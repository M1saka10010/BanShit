package app

import (
	"context"
	"errors"
	tgcore "github.com/M1saka10010/BanShit/internal/telegram"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

var telegramPhone = regexp.MustCompile(`^\+[1-9][0-9]{5,14}$`)
var telegramCode = regexp.MustCompile(`^[0-9]{4,8}$`)

// Caller holds tgMu. No Telegram connection exists until explicitly needed.
func (a *App) ensureTelegram(c tgcore.TelegramConfig) error {
	if a.ctx.Err() != nil {
		return errors.New("服务正在关闭")
	}
	if c.APIID == 0 || c.APIHash == "" {
		return errors.New("请先保存 Telegram API ID 和 API Hash")
	}
	if a.tg == nil {
		service, err := a.tgFactory(a.ctx, c, a.tgDir)
		if err != nil {
			return errors.New("Telegram 客户端初始化失败，请检查配置及会话目录权限")
		}
		a.tg = service
	}
	return nil
}

func (a *App) telegramReply(w http.ResponseWriter, c tgcore.TelegramConfig, state tgcore.TelegramState) {
	reply(w, 200, map[string]any{"enabled": c.Enabled, "apiId": c.APIID, "hasApiHash": c.APIHash != "", "state": state.State, "authorized": state.Authorized, "error": state.Error, "delivery": state.Delivery, "deliveryMessage": state.DeliveryMessage, "nextDelivery": state.NextDelivery, "resendAfter": state.ResendAfter, "canResend": state.CanResend, "codeLength": state.CodeLength})
}

func (a *App) telegramAdmin(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/admin/telegram")
	if !((path == "" && r.Method == "GET") || (path == "/config" && r.Method == "PUT") || ((path == "/send-code" || path == "/resend-code" || path == "/sign-in" || path == "/password" || path == "/logout" || path == "/reset") && r.Method == "POST")) {
		fail(w, 404, "未找到")
		return
	}
	if r.Method != "GET" && a.limited("telegram:"+a.clientIP(r), 15, time.Minute) {
		fail(w, 429, "Telegram 操作过于频繁，请稍后重试")
		return
	}
	var input struct {
		tgcore.TelegramConfig
		Phone    string
		Code     string
		Password string
		Confirm  bool
	}
	if r.Method != "GET" {
		controller := http.NewResponseController(w)
		_ = controller.SetReadDeadline(time.Now().Add(15 * time.Second))
		if err := decode(w, r, &input); err != nil {
			fail(w, 400, "请求格式错误")
			return
		}
		_ = controller.SetReadDeadline(time.Time{})
	}
	if !a.tgMu.TryLock() {
		fail(w, 409, "Telegram 正在执行其他操作，请稍后重试")
		return
	}
	defer a.tgMu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	stop := context.AfterFunc(a.ctx, cancel)
	defer stop()
	c := a.snapshot().Telegram
	if path == "/reset" {
		if !input.Confirm {
			fail(w, 400, "请确认仅重置本地会话，远端授权不会被撤销")
			return
		}
		if a.tg != nil {
			a.tg.Close()
			a.tg = nil
		}
		if err := os.Rename(a.tgDir, a.tgDir+"-archived-"+token(8)); err != nil && !os.IsNotExist(err) {
			fail(w, 500, "归档会话失败，请检查目录权限")
			return
		}
		a.telegramReply(w, c, tgcore.TelegramState{State: "idle"})
		return
	}
	if path == "/config" {
		next := input.TelegramConfig
		next.APIHash = strings.TrimSpace(next.APIHash)
		if next.APIHash == "" && next.APIID == c.APIID {
			next.APIHash = c.APIHash
		}
		if err := tgcore.ValidateTelegramConfig(next); err != nil {
			fail(w, 400, err.Error())
			return
		}
		changed := next.APIID != c.APIID || next.APIHash != c.APIHash
		_, sessionErr := os.Stat(a.tgDir)
		if changed && c.APIID != 0 && c.APIHash != "" && (a.tg != nil || !os.IsNotExist(sessionErr)) {
			if err := a.ensureTelegram(c); err != nil {
				fail(w, 409, err.Error())
				return
			}
			state, err := a.tg.Status(ctx)
			if err != nil || state.Authorized || state.State == "code" || state.State == "password" || state.State == "unsupported" {
				fail(w, 409, "更换应用凭据前请先退出 Telegram 会话，并确认连接正常")
				return
			}
		}
		a.mu.Lock()
		full := a.config
		full.Telegram = next
		err := a.save(full)
		a.mu.Unlock()
		if err != nil {
			fail(w, 500, "保存 Telegram 配置失败")
			return
		}
		if changed && a.tg != nil {
			a.tg.Close()
			a.tg = nil
		}
		state := tgcore.TelegramState{State: "idle"}
		if !changed && a.tg == nil && next.APIID != 0 {
			state = tgcore.TelegramState{State: "connecting"}
		}
		if !changed && a.tg != nil {
			if current, e := a.tg.Status(ctx); e == nil {
				state = current
			} else {
				state = tgcore.TelegramState{State: "error", Error: e.Error()}
			}
		}
		a.telegramReply(w, next, state)
		return
	}
	if path == "" && (c.APIID == 0 || c.APIHash == "") {
		a.telegramReply(w, c, tgcore.TelegramState{State: "idle"})
		return
	}
	if err := a.ensureTelegram(c); err != nil {
		fail(w, 400, err.Error())
		return
	}
	var state tgcore.TelegramState
	var err error
	switch path {
	case "":
		state, err = a.tg.Status(ctx)
	case "/resend-code":
		state, err = a.tg.ResendCode(ctx)
	case "/send-code":
		if !telegramPhone.MatchString(strings.TrimSpace(input.Phone)) {
			fail(w, 400, "手机号需包含 + 和国家区号")
			return
		}
		state, err = a.tg.SendCode(ctx, strings.TrimSpace(input.Phone))
	case "/sign-in":
		if !telegramCode.MatchString(strings.TrimSpace(input.Code)) {
			fail(w, 400, "请输入有效验证码")
			return
		}
		state, err = a.tg.SignIn(ctx, strings.TrimSpace(input.Code))
	case "/password":
		if input.Password == "" || len(input.Password) > 1024 {
			fail(w, 400, "请输入有效两步验证密码")
			return
		}
		state, err = a.tg.Password(ctx, input.Password)
		input.Password = ""
	case "/logout":
		err = a.tg.Logout(ctx)
		if err == nil {
			a.tg.Close()
			a.tg = nil
			state = tgcore.TelegramState{State: "idle"}
		}
	}
	if err != nil {
		fail(w, 502, err.Error())
		return
	}
	a.telegramReply(w, c, state)
}

func (a *App) collectTelegram(c Config, channel string) ([]Meme, error) {
	a.tgMu.Lock()
	defer a.tgMu.Unlock()
	if a.snapshot().Telegram != c.Telegram {
		return nil, errors.New("Telegram 配置已改变，请重新采集")
	}
	if err := a.ensureTelegram(c.Telegram); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 3*time.Minute)
	defer cancel()
	posts, err := a.tg.Collect(ctx, channel)
	if posts == nil {
		return nil, err
	}
	memes := make([]Meme, len(posts))
	for i, post := range posts {
		memes[i] = Meme{ID: post.ID, Text: post.Text, Images: post.Images, Channel: post.Channel, URL: post.URL, CreatedAt: post.CreatedAt}
	}
	return memes, err
}
