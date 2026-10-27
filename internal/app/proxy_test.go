package app

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestTrustedProxyAddresses(t *testing.T) {
	a := testApp(t)
	for _, tc := range []struct {
		peer, forwarded, want string
		trusted               []string
	}{
		{"127.0.0.1:1234", "192.0.2.10", "127.0.0.1", nil},
		{"127.0.0.1:1234", "192.0.2.10", "192.0.2.10", []string{"127.0.0.1"}},
		{"127.0.0.1:1234", "192.0.2.10, 10.0.0.2", "192.0.2.10", []string{"127.0.0.1", "10.0.0.0/24"}},
		{"127.0.0.1:1234", "192.0.2.10, 10.0.0.2", "10.0.0.2", []string{"127.0.0.1"}},
		{"[::1]:1234", "2001:db8::10", "2001:db8::10", []string{"::1"}},
		{"127.0.0.1:1234", "", "127.0.0.1", []string{"127.0.0.1"}},
	} {
		a.config.TrustedProxies = tc.trusted
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = tc.peer
		r.Header.Set("X-Forwarded-For", tc.forwarded)
		if got := a.clientIP(r); got != tc.want {
			t.Fatalf("%+v: got %s", tc, got)
		}
	}
	for _, raw := range []string{"invalid", "0.0.0.0/0", "::/0", "127.0.0.1:80"} {
		if validateTrustedProxies([]string{raw}) == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
}

func TestAdminSavePreservesTrustedProxies(t *testing.T) {
	a := testApp(t)
	a.config.TrustedProxies = []string{"127.0.0.1"}
	session := login(t, a)
	c := a.snapshot()
	c.TrustedProxies = nil
	b, _ := json.Marshal(c)
	checkCode(t, req(a, "PUT", "/api/admin/config", string(b), session), 200)
	if got := a.snapshot().TrustedProxies; len(got) != 1 || got[0] != "127.0.0.1" {
		t.Fatal(got)
	}
}
