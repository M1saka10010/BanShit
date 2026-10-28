package app

import (
	"context"
	"encoding/json"
	"errors"
	tgcore "github.com/M1saka10010/BanShit/internal/telegram"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeTelegram struct {
	state     tgcore.TelegramState
	logoutErr error
	closed    bool
	posts     []tgcore.Post
	calls     int
}

func (f *fakeTelegram) ResendCode(context.Context) (tgcore.TelegramState, error) {
	f.calls++
	return f.state, nil
}

func (f *fakeTelegram) Status(context.Context) (tgcore.TelegramState, error) { return f.state, nil }
func (f *fakeTelegram) SendCode(context.Context, string) (tgcore.TelegramState, error) {
	f.calls++
	f.state = tgcore.TelegramState{State: "code"}
	return f.state, nil
}
func (f *fakeTelegram) SignIn(context.Context, string) (tgcore.TelegramState, error) {
	f.state = tgcore.TelegramState{State: "password"}
	return f.state, nil
}
func (f *fakeTelegram) Password(context.Context, string) (tgcore.TelegramState, error) {
	f.state = tgcore.TelegramState{State: "authorized", Authorized: true}
	return f.state, nil
}
func (f *fakeTelegram) Logout(context.Context) error {
	if f.logoutErr == nil {
		f.state = tgcore.TelegramState{State: "idle"}
	}
	return f.logoutErr
}
func (f *fakeTelegram) Collect(context.Context, string) ([]tgcore.Post, error) { return f.posts, nil }
func (f *fakeTelegram) Close()                                                 { f.closed = true }

func TestTelegramAdminFlow(t *testing.T) {
	a := testApp(t)
	fake := &fakeTelegram{state: tgcore.TelegramState{State: "idle"}}
	a.tgFactory = func(context.Context, tgcore.TelegramConfig, string) (tgcore.TelegramService, error) { return fake, nil }
	checkCode(t, req(a, "GET", "/api/admin/telegram", ""), 401)
	session := login(t, a)
	c := tgcore.TelegramConfig{APIID: 12345, APIHash: strings.Repeat("a", 32), Enabled: true}
	b, _ := json.Marshal(c)
	checkCode(t, req(a, "PUT", "/api/admin/telegram/config", string(b), session), 200)
	if a.tg != nil {
		t.Fatal("saving config started Telegram")
	}
	status := req(a, "GET", "/api/admin/telegram", "", session)
	checkCode(t, status, 200)
	if strings.Contains(status.Body.String(), c.APIHash) {
		t.Fatal("hash exposed")
	}
	checkCode(t, req(a, "POST", "/api/admin/telegram/send-code", `{"phone":"+12345678901"}`, session), 200)
	checkCode(t, req(a, "POST", "/api/admin/telegram/sign-in", `{"code":"12345"}`, session), 200)
	checkCode(t, req(a, "POST", "/api/admin/telegram/password", `{"password":"sensitive-password"}`, session), 200)
	c.APIHash = ""
	b, _ = json.Marshal(c)
	status = req(a, "PUT", "/api/admin/telegram/config", string(b), session)
	checkCode(t, status, 200)
	if !strings.Contains(status.Body.String(), `"authorized":true`) {
		t.Fatal(status.Body.String())
	}
	c.APIID++
	checkCode(t, req(a, "PUT", "/api/admin/telegram/config", `{"apiId":12346,"apiHash":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","enabled":true}`, session), 409)
	raw, _ := os.ReadFile(a.configPath)
	if strings.Contains(string(raw), "sensitive-password") || strings.Contains(string(raw), "12345678901") {
		t.Fatal("auth secrets persisted")
	}
	fake.logoutErr = errors.New("offline")
	checkCode(t, req(a, "POST", "/api/admin/telegram/logout", "{}", session), 502)
	if fake.closed {
		t.Fatal("failed logout discarded session")
	}
	fake.logoutErr = nil
	checkCode(t, req(a, "POST", "/api/admin/telegram/logout", "{}", session), 200)
	if !fake.closed || a.tg != nil {
		t.Fatal("logout did not close")
	}
}

func TestTelegramGuards(t *testing.T) {
	a := testApp(t)
	session := login(t, a)
	fake := &fakeTelegram{state: tgcore.TelegramState{State: "idle"}}
	a.config.Telegram = tgcore.TelegramConfig{APIID: 12345, APIHash: strings.Repeat("a", 32)}
	a.tg = fake
	r := httptest.NewRequest("POST", "/api/admin/telegram/send-code", strings.NewReader(`{"phone":"+12345678901"}`))
	r.AddCookie(session)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://evil.example")
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	checkCode(t, w, http.StatusForbidden)
	checkCode(t, req(a, "POST", "/api/admin/telegram/send-code", `{"phone":"invalid"}`, session), 400)
	a.tgMu.Lock()
	w = req(a, "GET", "/api/admin/telegram", "", session)
	a.tgMu.Unlock()
	checkCode(t, w, 409)
	if fake.calls != 0 {
		t.Fatal("guard bypass")
	}
	// General settings must never erase the separately managed Telegram config.
	c := a.snapshot()
	c.Telegram = tgcore.TelegramConfig{}
	b, _ := json.Marshal(c)
	checkCode(t, req(a, "PUT", "/api/admin/config", string(b), session), 200)
	if a.snapshot().Telegram.APIHash == "" {
		t.Fatal("Telegram config lost")
	}
}

func TestRefreshTelegramMode(t *testing.T) {
	a := testApp(t)
	model, c := rankingModel(t, nil, nil)
	a.client = model.client
	a.config.AI = c.AI
	a.config.Telegram = tgcore.TelegramConfig{Enabled: true, APIID: 12345, APIHash: strings.Repeat("a", 32)}
	a.config.AI.Model = "test"
	a.tg = &fakeTelegram{posts: []tgcore.Post{{ID: "memes/9", Text: "account post", Channel: "memes", Images: []string{}, CreatedAt: "2026-01-01T00:00:00.000Z"}}}
	if err := a.refresh(a.snapshot()); err != nil {
		t.Fatal(err)
	}
	w := req(a, "GET", "/api/memes", "")
	checkCode(t, w, 200)
	if !strings.Contains(w.Body.String(), "account post") {
		t.Fatal(w.Body.String())
	}
	old := a.snapshot()
	a.config.Telegram.Enabled = false
	if _, err := a.collectTelegram(old, "memes"); err == nil {
		t.Fatal("stale config accepted")
	}
}

func TestTelegramResetRecovery(t *testing.T) {
	a := testApp(t)
	session := login(t, a)
	a.config.Telegram = tgcore.TelegramConfig{APIID: 12345, APIHash: strings.Repeat("a", 32)}
	a.tgFactory = func(context.Context, tgcore.TelegramConfig, string) (tgcore.TelegramService, error) {
		return nil, errors.New("broken")
	}
	if err := os.MkdirAll(a.tgDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a.tgDir, "session.json"), []byte("old session"), 0600); err != nil {
		t.Fatal(err)
	}
	checkCode(t, req(a, "POST", "/api/admin/telegram/reset", "{}", session), 400)
	checkCode(t, req(a, "POST", "/api/admin/telegram/reset", `{"confirm":true}`, session), 200)
	paths, err := filepath.Glob(a.tgDir + "-archived-*/session.json")
	if err != nil || len(paths) != 1 {
		t.Fatalf("%v %v", paths, err)
	}
	checkCode(t, req(a, "PUT", "/api/admin/telegram/config", `{"apiId":12346,"apiHash":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`, session), 200)
	if a.snapshot().Telegram.APIID != 12346 {
		t.Fatal("recovery failed")
	}
}

func TestTelegramUninitializedState(t *testing.T) {
	a := testApp(t)
	session := login(t, a)
	a.config.Telegram = tgcore.TelegramConfig{APIID: 12345, APIHash: strings.Repeat("a", 32)}
	b, _ := json.Marshal(a.config.Telegram)
	w := req(a, "PUT", "/api/admin/telegram/config", string(b), session)
	checkCode(t, w, 200)
	if !strings.Contains(w.Body.String(), "connecting") {
		t.Fatal(w.Body.String())
	}
}

type telegramSlowBody struct {
	entered chan struct{}
	release chan struct{}
}

func (b *telegramSlowBody) Read([]byte) (int, error) {
	select {
	case b.entered <- struct{}{}:
	default:
	}
	<-b.release
	return 0, io.EOF
}
func (b *telegramSlowBody) Close() error { return nil }
func TestTelegramBodyDoesNotHoldLock(t *testing.T) {
	a := testApp(t)
	session := login(t, a)
	body := &telegramSlowBody{entered: make(chan struct{}, 1), release: make(chan struct{})}
	r := httptest.NewRequest("POST", "/api/admin/telegram/send-code", nil)
	r.Body = body
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(session)
	done := make(chan struct{})
	go func() { defer close(done); a.ServeHTTP(httptest.NewRecorder(), r) }()
	defer func() { close(body.release); <-done }()
	select {
	case <-body.entered:
	case <-time.After(time.Second):
		t.Fatal("body not read")
	}
	if !a.tgMu.TryLock() {
		t.Fatal("slow body holds Telegram lock")
	}
	a.tgMu.Unlock()
}

func TestTelegramDeliveryResponse(t *testing.T) {
	a := testApp(t)
	session := login(t, a)
	a.config.Telegram = tgcore.TelegramConfig{APIID: 12345, APIHash: strings.Repeat("a", 32)}
	f := &fakeTelegram{state: tgcore.TelegramState{State: "code", Delivery: "app", DeliveryMessage: "请查看 Telegram 官方服务通知", NextDelivery: "sms", ResendAfter: 60, CodeLength: 5}}
	a.tg = f
	checkCode(t, req(a, "POST", "/api/admin/telegram/resend-code", "{}"), 401)
	w := req(a, "GET", "/api/admin/telegram", "", session)
	checkCode(t, w, 200)
	var state tgcore.TelegramState
	if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if state.Delivery != "app" || state.NextDelivery != "sms" || state.ResendAfter != 60 || state.CodeLength != 5 {
		t.Fatal(state)
	}
	checkCode(t, req(a, "POST", "/api/admin/telegram/resend-code", "{}", session), 200)
	if f.calls != 1 {
		t.Fatal("resend route not dispatched")
	}
}
