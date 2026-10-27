package telegram

import (
	"context"
	"errors"
	"fmt"
	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/dcs"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

type TelegramConfig struct {
	Enabled bool   `json:"enabled"`
	APIID   int    `json:"apiId"`
	APIHash string `json:"apiHash,omitempty"`
}
type TelegramState struct {
	Delivery        string `json:"delivery"`
	DeliveryMessage string `json:"deliveryMessage"`
	NextDelivery    string `json:"nextDelivery"`
	ResendAfter     int    `json:"resendAfter"`
	CanResend       bool   `json:"canResend"`
	CodeLength      int    `json:"codeLength"`
	State           string `json:"state"`
	Authorized      bool   `json:"authorized"`
	Error           string `json:"error,omitempty"`
}
type TelegramService interface {
	Status(context.Context) (TelegramState, error)
	SendCode(context.Context, string) (TelegramState, error)
	ResendCode(context.Context) (TelegramState, error)
	SignIn(context.Context, string) (TelegramState, error)
	Password(context.Context, string) (TelegramState, error)
	Logout(context.Context) error
	Collect(context.Context, string) ([]Post, error)
	Close()
}

func ValidateTelegramConfig(c TelegramConfig) error {
	if c.APIID == 0 && c.APIHash == "" && !c.Enabled {
		return nil
	}
	if c.APIID <= 0 || c.APIID > 2147483647 || !regexp.MustCompile("^[a-fA-F0-9]{32}$").MatchString(c.APIHash) {
		return errors.New("Telegram API ID/hash 配置无效")
	}
	return nil
}

type telegramService struct {
	mu                                       sync.Mutex
	ctx                                      context.Context
	cancel                                   context.CancelFunc
	config                                   TelegramConfig
	storage                                  *telegramSessionStorage
	client                                   *telegram.Client
	stop                                     context.CancelFunc
	done, ready                              chan struct{}
	state, phone, hash                       string
	expires                                  time.Time
	expiry                                   *time.Timer
	delivery, deliveryMessage, deliveryError string
	nextType                                 tg.AuthCodeTypeClass
	codeLength                               int
	resendAt, floodUntil                     time.Time
}

func NewTelegramService(ctx context.Context, c TelegramConfig, dataDir string) (TelegramService, error) {
	if err := ValidateTelegramConfig(c); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	s := &telegramService{ctx: ctx, cancel: cancel, config: c, state: "idle"}
	if c.APIID != 0 {
		if err := os.MkdirAll(dataDir, 0700); err != nil {
			cancel()
			return nil, errors.New("无法创建 Telegram 会话目录")
		}
		if err := os.Chmod(dataDir, 0700); err != nil {
			cancel()
			return nil, errors.New("无法保护 Telegram 会话目录")
		}
		s.storage = &telegramSessionStorage{path: filepath.Join(dataDir, "session.json")}
	}
	return s, nil
}
func telegramSafeError(err error) error {
	if err == nil {
		return nil
	}
	var local telegramDeliveryError
	if errors.As(err, &local) {
		return local
	}
	if d, ok := tgerr.AsFloodWait(err); ok {
		return fmt.Errorf("Telegram 请求过于频繁，请等待 %d 秒", int(d.Seconds()))
	}
	for code, message := range map[string]string{"PHONE_CODE_INVALID": "验证码无效，请重试", "PHONE_CODE_EXPIRED": "验证码已过期，请重新发送", "PHONE_NUMBER_INVALID": "手机号格式无效", "PASSWORD_HASH_INVALID": "二步验证密码无效", "API_ID_INVALID": "Telegram API 配置无效"} {
		if tgerr.Is(err, code) {
			return errors.New(message)
		}
	}
	if errors.Is(err, auth.ErrPasswordInvalid) {
		return errors.New("二步验证密码无效")
	}
	if errors.Is(err, context.Canceled) {
		return errors.New("Telegram 操作已取消")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return errors.New("Telegram 操作超时，请稍后重试")
	}
	return errors.New("Telegram 操作失败，请检查配置、登录信息或网络后重试")
}
func (s *telegramService) operation(ctx context.Context) (context.Context, context.CancelFunc, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	stop := context.AfterFunc(s.ctx, cancel)
	cleanup := func() { stop(); cancel() }
	if s.ctx.Err() != nil {
		cleanup()
		return ctx, func() {}, context.Canceled
	}
	if s.storage == nil {
		cleanup()
		return ctx, func() {}, errors.New("not configured")
	}
	if s.client != nil {
		select {
		case <-s.done:
			s.stop()
			s.client = nil
			s.clearPending()
			s.state = "idle"
		default:
		}
	}
	if s.client == nil {
		runCtx, runCancel := context.WithCancel(s.ctx)
		s.stop = runCancel
		s.done = make(chan struct{})
		s.ready = make(chan struct{})
		s.client = telegram.NewClient(s.config.APIID, s.config.APIHash, telegram.Options{SessionStorage: s.storage, NoUpdates: true, Resolver: dcs.Plain(dcs.PlainOptions{Dial: telegramProxyDial()})})
		client, done, ready := s.client, s.done, s.ready
		go func() {
			defer close(done)
			_ = client.Run(runCtx, func(ctx context.Context) error { close(ready); <-ctx.Done(); return ctx.Err() })
		}()
	}
	select {
	case <-ctx.Done():
		cleanup()
		return ctx, func() {}, ctx.Err()
	case <-s.done:
		cleanup()
		return ctx, func() {}, errors.New("connection ended")
	case <-s.ready:
		return ctx, cleanup, nil
	}
}
func (s *telegramService) clearPending() {
	s.delivery, s.deliveryMessage, s.deliveryError = "", "", ""
	s.nextType = nil
	s.codeLength = 0
	s.resendAt = time.Time{}
	s.phone = ""
	s.hash = ""
	s.expires = time.Time{}
	if s.expiry != nil {
		s.expiry.Stop()
		s.expiry = nil
	}
}
func (s *telegramService) snapshot(err error) (TelegramState, error) {
	if !s.expires.IsZero() && !time.Now().Before(s.expires) {
		s.clearPending()
		s.state = "idle"
	}
	safe := telegramSafeError(err)
	st := TelegramState{State: s.state, Authorized: s.state == "authorized"}
	s.deliverySnapshot(&st, time.Now())
	if safe != nil {
		st.Error = safe.Error()
	}
	return st, safe
}
func (s *telegramService) Status(ctx context.Context) (TelegramState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.storage == nil {
		return s.snapshot(nil)
	}
	if s.client == nil {
		if _, err := os.Stat(s.storage.path); os.IsNotExist(err) {
			return s.snapshot(nil)
		}
	}
	ctx, cancel, err := s.operation(ctx)
	defer cancel()
	if err != nil {
		return s.snapshot(err)
	}
	status, err := s.client.Auth().Status(ctx)
	if err == nil {
		if status.Authorized {
			s.clearPending()
			s.state = "authorized"
		} else if s.state == "authorized" {
			s.state = "idle"
		}
	}
	return s.snapshot(err)
}
func (s *telegramService) SendCode(ctx context.Context, phone string) (TelegramState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == "authorized" {
		return s.snapshot(nil)
	}
	s.expirePending(time.Now())
	if time.Now().Before(s.floodUntil) {
		return s.snapshot(telegramDeliveryError("Telegram 请求过于频繁，请等待冷却结束"))
	}
	if !s.expires.IsZero() {
		return s.snapshot(telegramDeliveryError("当前登录请求尚未过期，请使用重发或继续登录"))
	}
	if !regexp.MustCompile("^[+][0-9]{5,15}$").MatchString(phone) {
		return s.snapshot(errors.New("invalid phone"))
	}
	ctx, cancel, err := s.operation(ctx)
	defer cancel()
	if err != nil {
		return s.snapshot(err)
	}
	status, err := s.client.Auth().Status(ctx)
	if err != nil {
		return s.snapshot(err)
	}
	if status.Authorized {
		s.clearPending()
		s.state = "authorized"
		return s.snapshot(nil)
	}
	result, err := s.client.Auth().SendCode(ctx, phone, auth.SendCodeOptions{})
	if err != nil {
		s.deliveryFailure(err)
		return s.snapshot(err)
	}
	s.acceptDelivery(phone, result, time.Now())
	return s.snapshot(nil)
}
func (s *telegramService) SignIn(ctx context.Context, code string) (TelegramState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != "code" {
		return s.snapshot(errors.New("code not expected"))
	}
	if !time.Now().Before(s.expires) {
		s.clearPending()
		s.state = "idle"
		return s.snapshot(errors.New("code expired"))
	}
	ctx, cancel, err := s.operation(ctx)
	defer cancel()
	if err != nil {
		return s.snapshot(err)
	}
	if s.state != "code" || s.phone == "" || s.hash == "" || !time.Now().Before(s.expires) {
		return s.snapshot(errors.New("login restarted"))
	}
	_, err = s.client.Auth().SignIn(ctx, s.phone, code, s.hash)
	if errors.Is(err, auth.ErrPasswordAuthNeeded) {
		s.clearPending()
		s.expires = time.Now().Add(10 * time.Minute)
		s.state = "password"
		return s.snapshot(nil)
	}
	if err == nil {
		s.clearPending()
		s.state = "authorized"
	}
	return s.snapshot(err)
}
func (s *telegramService) Password(ctx context.Context, password string) (TelegramState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != "password" || !time.Now().Before(s.expires) {
		return s.snapshot(errors.New("password not expected"))
	}
	ctx, cancel, err := s.operation(ctx)
	defer cancel()
	if err != nil {
		return s.snapshot(err)
	}
	if s.state != "password" {
		return s.snapshot(errors.New("login restarted"))
	}
	_, err = s.client.Auth().Password(ctx, password)
	if err == nil {
		s.clearPending()
		s.state = "authorized"
	}
	return s.snapshot(err)
}
func (s *telegramService) Logout(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.storage == nil {
		return nil
	}
	ctx, cancel, err := s.operation(ctx)
	defer cancel()
	if err != nil {
		return telegramSafeError(err)
	}
	if _, err = s.client.API().AuthLogOut(ctx); err != nil && !auth.IsUnauthorized(err) {
		return telegramSafeError(err)
	}
	s.stop()
	<-s.done
	s.client = nil
	s.clearPending()
	s.state = "idle"
	return telegramSafeError(s.storage.remove())
}
func (s *telegramService) Close() {
	s.cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clearPending()
	if s.done != nil {
		<-s.done
	}
}

type telegramSessionStorage struct {
	mu   sync.Mutex
	path string
}

func (s *telegramSessionStorage) LoadSession(ctx context.Context) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := os.Chmod(s.path, 0600); os.IsNotExist(err) {
		return nil, session.ErrNotFound
	} else if err != nil {
		return nil, err
	}
	return os.ReadFile(s.path)
}
func (s *telegramSessionStorage) StoreSession(ctx context.Context, b []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".session-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), s.path)
}
func (s *telegramSessionStorage) remove() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := os.Remove(s.path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
