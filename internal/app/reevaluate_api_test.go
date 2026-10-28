package app

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestReevaluateGuard(t *testing.T) {
	a := testApp(t)
	session := login(t, a)
	checkCode(t, req(a, "POST", "/api/admin/reevaluate", ""), 401)
	a.mu.Lock()
	a.status.Running = true
	a.mu.Unlock()
	checkCode(t, req(a, "POST", "/api/admin/reevaluate", "", session), 409)
	a.mu.Lock()
	a.status.Running = false
	a.mu.Unlock()
	r := httptest.NewRequest("POST", "/api/admin/reevaluate", nil)
	r.Header.Set("Origin", "https://untrusted.example")
	r.AddCookie(session)
	denied := httptest.NewRecorder()
	a.ServeHTTP(denied, r)
	checkCode(t, denied, 403)
}

func TestReevaluateDoesNotDelayCollection(t *testing.T) {
	a := testApp(t)
	a.mu.Lock()
	a.config.Channels = nil
	before := time.Now().Add(-24 * time.Hour)
	a.lastRefresh = before
	a.mu.Unlock()
	if !a.launchReevaluate() {
		t.Fatal("reevaluation not launched")
	}
	a.wg.Wait()
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.status.LastError != "" {
		t.Fatal(a.status.LastError)
	}
	if !a.lastRefresh.Equal(before) {
		t.Fatal("reevaluation postponed the next collection")
	}
}
