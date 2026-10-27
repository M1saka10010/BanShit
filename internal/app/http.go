package app

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/M1saka10010/BanShit/public"
	"io/fs"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func token(n int) string {
	b := make([]byte, n)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func cookie(r *http.Request, name string) string {
	c, e := r.Cookie(name)
	if e != nil {
		return ""
	}
	return c.Value
}
func (a *App) setCookie(w http.ResponseWriter, name, value string, age int) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", HttpOnly: true, Secure: a.snapshot().SecureCookie, SameSite: http.SameSiteStrictMode, MaxAge: age})
}
func (a *App) limited(key string, max int, d time.Duration) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	for k, v := range a.limits {
		if now.After(v.Until) {
			delete(a.limits, k)
		}
	}
	v := a.limits[key]
	if v.Until.IsZero() {
		v.Until = now.Add(d)
	}
	v.Count++
	a.limits[key] = v
	return v.Count > max
}
func reply(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, code int, e string) { reply(w, code, map[string]string{"error": e}) }
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		return errors.New("需要 application/json")
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536))
	return d.Decode(v)
}
func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: blob:; style-src 'self'; script-src 'self'; connect-src 'self' data:; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
	if r.Method != "GET" && r.Method != "HEAD" {
		o := r.Header.Get("Origin")
		u, e := url.Parse(o)
		if (o != "" && (e != nil || u.Host != r.Host)) || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			fail(w, 403, "拒绝跨站请求")
			return
		}
	}
	path := r.URL.Path
	if path == "/api/login" && r.Method == "POST" {
		if a.limited("login:"+a.clientIP(r), 10, 15*time.Minute) {
			fail(w, 429, "登录尝试过多")
			return
		}
		var v struct{ Password string }
		if e := decode(w, r, &v); e != nil {
			fail(w, 400, e.Error())
			return
		}
		x, y := sha256.Sum256([]byte(v.Password)), sha256.Sum256([]byte(a.snapshot().AdminPassword))
		if subtle.ConstantTimeCompare(x[:], y[:]) != 1 {
			fail(w, 401, "密码错误")
			return
		}
		t := token(32)
		a.mu.Lock()
		for k, until := range a.sessions {
			if time.Now().After(until) {
				delete(a.sessions, k)
			}
		}
		a.sessions[t] = time.Now().Add(12 * time.Hour)
		a.mu.Unlock()
		a.setCookie(w, "session", t, 43200)
		reply(w, 200, map[string]bool{"ok": true})
		return
	}
	if path == "/api/logout" && r.Method == "POST" {
		a.mu.Lock()
		delete(a.sessions, cookie(r, "session"))
		a.mu.Unlock()
		a.setCookie(w, "session", "", -1)
		reply(w, 200, map[string]bool{"ok": true})
		return
	}
	if strings.HasPrefix(path, "/api/admin/") {
		a.mu.Lock()
		until := a.sessions[cookie(r, "session")]
		a.mu.Unlock()
		if !time.Now().Before(until) {
			fail(w, 401, "请先登录")
			return
		}
		a.admin(w, r)
		return
	}
	if path == "/api/memes" && r.Method == "GET" {
		a.feed(w, r)
		return
	}
	if strings.HasPrefix(path, "/api/memes/") && strings.HasSuffix(path, "/copy") && r.Method == "POST" {
		a.copyMeme(w, r)
		return
	}
	if r.Method == "GET" {
		file, ok := map[string]string{"/": "index.html", "/admin": "admin.html", "/admin/": "admin.html"}[path]
		if !ok {
			file = strings.TrimPrefix(path, "/")
			if !fs.ValidPath(file) {
				fail(w, 404, "未找到")
				return
			}
		}
		b, e := fs.ReadFile(public.Assets, file)
		if e != nil {
			fail(w, 404, "未找到")
			return
		}
		mime := "application/octet-stream"
		switch {
		case strings.HasSuffix(file, ".html"):
			mime = "text/html; charset=utf-8"
		case strings.HasSuffix(file, ".js"):
			mime = "text/javascript; charset=utf-8"
		case strings.HasSuffix(file, ".css"):
			mime = "text/css; charset=utf-8"
		case strings.HasSuffix(file, ".svg"):
			mime = "image/svg+xml"
		}
		w.Header().Set("Content-Type", mime)
		_, _ = w.Write(b)
		return
	}
	fail(w, 404, "未找到")
}
