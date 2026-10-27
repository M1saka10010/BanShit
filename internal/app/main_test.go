package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func testApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	b, e := os.ReadFile("../../config.example.json")
	if e != nil {
		t.Fatal(e)
	}
	var c Config
	if e = json.Unmarshal(b, &c); e != nil {
		t.Fatal(e)
	}
	c.AdminPassword = "test-password-123"
	c.AI.APIKey = "test-secret"
	b, _ = json.Marshal(c)
	path := filepath.Join(dir, "config.json")
	if e = os.WriteFile(path, b, 0600); e != nil {
		t.Fatal(e)
	}
	a, e := NewApp(path, dir)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(a.Close)
	return a
}
func req(a *App, method, path, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	for _, c := range cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	return w
}
func checkCode(t *testing.T, w *httptest.ResponseRecorder, code int) {
	t.Helper()
	if w.Code != code {
		t.Fatalf("want %d got %d: %s", code, w.Code, w.Body.String())
	}
}
func login(t *testing.T, a *App) *http.Cookie {
	w := req(a, "POST", "/api/login", "{\"password\":\"test-password-123\"}")
	checkCode(t, w, 200)
	return w.Result().Cookies()[0]
}
func TestAPI(t *testing.T) {
	a := testApp(t)
	for _, p := range []string{"/", "/admin", "/theme.js", "/js/home.js", "/js/admin.js", "/css/base.css"} {
		checkCode(t, req(a, "GET", p, ""), 200)
	}
	checkCode(t, req(a, "GET", "/config.json", ""), 404)
	checkCode(t, req(a, "GET", "/api/admin/config", ""), 401)
	checkCode(t, req(a, "POST", "/api/login", "{\"password\":\"wrong\"}"), 401)
	session := login(t, a)
	w := req(a, "GET", "/api/admin/config", "", session)
	checkCode(t, w, 200)
	if strings.Contains(w.Body.String(), "test-secret") || strings.Contains(w.Body.String(), "test-password") {
		t.Fatal("secret exposed")
	}
	for i := 0; i < 20; i++ {
		_, e := a.db.Exec("INSERT INTO memes VALUES (?,?,?,?,?,?,?)", fmt.Sprintf("memes/%d", i), "meme", "[\"data:image/png;base64,aA==\"]", "memes", "https://t.me/memes/1", i, fmt.Sprintf("2026-01-%02dT00:00:00Z", i+1))
		if e != nil {
			t.Fatal(e)
		}
	}
	pool, e := a.rankingPool(a.snapshot())
	if e != nil {
		t.Fatal(e)
	}
	if e = a.publishFeed(a.snapshot(), pool[:16]); e != nil {
		t.Fatal(e)
	}
	w = req(a, "GET", "/api/memes", "")
	checkCode(t, w, 200)
	visitor := w.Result().Cookies()[0]
	var feed struct{ Memes []Meme }
	if e := json.Unmarshal(w.Body.Bytes(), &feed); e != nil {
		t.Fatal(e)
	}
	if len(feed.Memes) != 16 || feed.Memes[0].Score != 19 {
		t.Fatal("incorrect feed")
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := req(a, "POST", "/api/memes/memes%2F19/copy", "{\"kind\":\"image\"}", visitor)
			if w.Code != 200 {
				t.Errorf("copy: %s", w.Body.String())
			}
		}()
	}
	wg.Wait()
	var n int
	if e := a.db.QueryRow("SELECT count(*) FROM copies").Scan(&n); e != nil || n != 8 {
		t.Fatalf("operation log: %d %v", n, e)
	}
	c := a.snapshot()
	c.IntervalMinutes = 3
	c.AI.APIKey = ""
	b, _ := json.Marshal(c)
	checkCode(t, req(a, "PUT", "/api/admin/config", string(b), session), 200)
	if a.snapshot().AI.APIKey != "test-secret" {
		t.Fatal("key not retained")
	}
	c.IntervalMinutes = 0
	b, _ = json.Marshal(c)
	checkCode(t, req(a, "PUT", "/api/admin/config", string(b), session), 400)
	r := httptest.NewRequest("POST", "/api/login", strings.NewReader("{}"))
	r.Header.Set("Origin", "https://evil.example")
	w = httptest.NewRecorder()
	a.ServeHTTP(w, r)
	checkCode(t, w, 403)
	checkCode(t, req(a, "POST", "/api/logout", "", session), 200)
	checkCode(t, req(a, "GET", "/api/admin/config", "", session), 401)
}
func TestOptimizeAndModels(t *testing.T) {
	a := testApp(t)
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "models") {
			fmt.Fprint(w, "{\"data\":[{\"id\":\"vision\"}]}")
			return
		}
		fmt.Fprint(w, "{\"choices\":[{\"message\":{\"content\":\"偏好反差梗\"}}]}")
	}))
	defer mock.Close()
	a.config.AI.BaseURL = mock.URL + "/v1"
	a.config.AI.Model = "vision"
	_, e := a.db.Exec("INSERT INTO memes VALUES ('memes/1','test','[\"data:image/png;base64,aA==\"]','memes','https://t.me/memes/1',80,'2026-01-01T00:00:00Z'); INSERT INTO copies (memeId,kind,visitor,createdAt,imageIndex) VALUES ('memes/1','image','visitor','2026-01-01T00:00:00Z',0)")
	if e != nil {
		t.Fatal(e)
	}
	session := login(t, a)
	w := req(a, "POST", "/api/admin/models", "{}", session)
	checkCode(t, w, 200)
	if !strings.Contains(w.Body.String(), "vision") {
		t.Fatal("models missing")
	}
	if _, e = a.db.Exec("UPDATE memes SET images=? WHERE id='memes/1'", "[\""+previewFixture(t, 1, 1, 0, false)+"\"]"); e != nil {
		t.Fatal(e)
	}
	if e = a.optimize(a.snapshot()); e != nil {
		t.Fatal(e)
	}
	if a.snapshot().InterestPrompt != "偏好反差梗" || a.meta("optimizedCopyId") != "1" {
		t.Fatal("optimization not persisted")
	}
	b, e := os.ReadFile(a.configPath)
	if e != nil {
		t.Fatal(e)
	}
	var saved Config
	if e = json.Unmarshal(b, &saved); e != nil {
		t.Fatal(e)
	}
	if saved.InterestPrompt != "偏好反差梗" {
		t.Fatal("config not persisted")
	}
}
func TestValidationAndShutdown(t *testing.T) {
	a := testApp(t)
	c := a.snapshot()
	c.Channels = []string{"@Memes", "https://t.me/s/memes/"}
	if e := validate(&c); e != nil || len(c.Channels) != 1 {
		t.Fatalf("channels: %v %v", c.Channels, e)
	}
	c.AI.BaseURL = "file:///etc/passwd"
	if validate(&c) == nil {
		t.Fatal("invalid URL accepted")
	}
	a.StartScheduler()
	a.cancel()
	a.wg.Wait()
	if a.launch(false) {
		t.Fatal("launched after shutdown")
	}
}
