package telegram

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func cleanProxyEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"ALL_PROXY", "all_proxy", "HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "NO_PROXY", "no_proxy"} {
		t.Setenv(k, "")
	}
}

func TestTelegramHTTPProxy(t *testing.T) {
	cleanProxyEnv(t)
	seen := make(chan bool, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Method == "CONNECT" && r.Host == "149.154.167.50:443" && r.Header.Get("Proxy-Authorization") == "Basic dXNlcjpwYXNz"
		c, b, e := w.(http.Hijacker).Hijack()
		if e != nil {
			return
		}
		defer c.Close()
		b.WriteString("HTTP/1.1 200 Connection established" + string([]byte{13, 10, 13, 10}) + "hello")
		b.Flush()
		io.Copy(c, c)
	}))
	defer server.Close()
	t.Setenv("HTTPS_PROXY", strings.Replace(server.URL, "http://", "http://user:pass@", 1))
	conn, err := telegramProxyDial()(context.Background(), "tcp", "149.154.167.50:443")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(time.Second))
	b := make([]byte, 5)
	if _, err = io.ReadFull(conn, b); err != nil || string(b) != "hello" {
		t.Fatalf("%q %v", b, err)
	}
	if !<-seen {
		t.Fatal("CONNECT target or auth incorrect")
	}
}

func TestTelegramProxyRejectAndCancel(t *testing.T) {
	cleanProxyEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(407) }))
	defer server.Close()
	t.Setenv("ALL_PROXY", server.URL)
	if c, e := telegramProxyDial()(context.Background(), "tcp", "149.154.167.50:443"); e == nil {
		c.Close()
		t.Fatal("proxy rejection bypassed")
	}
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		c, e := listener.Accept()
		if e != nil {
			return
		}
		defer c.Close()
		io.Copy(io.Discard, c)
	}()
	t.Setenv("ALL_PROXY", "http://"+listener.Addr().String())
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if c, e := telegramProxyDial()(ctx, "tcp", "149.154.167.50:443"); e == nil {
		c.Close()
		t.Fatal("timeout ignored")
	}
	<-done
}

func TestTelegramSOCKSProxy(t *testing.T) {
	cleanProxyEnv(t)
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	done := make(chan error, 1)
	go func() {
		c, e := listener.Accept()
		if e != nil {
			done <- e
			return
		}
		defer c.Close()
		c.SetDeadline(time.Now().Add(time.Second))
		r := bufio.NewReader(c)
		b := make([]byte, 2)
		_, e = io.ReadFull(r, b)
		if e != nil {
			done <- e
			return
		}
		methods := make([]byte, int(b[1]))
		io.ReadFull(r, methods)
		c.Write([]byte{5, 0})
		b = make([]byte, 10)
		_, e = io.ReadFull(r, b)
		if e != nil {
			done <- e
			return
		}
		c.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 80})
		c.Write([]byte("ok"))
		done <- nil
	}()
	t.Setenv("ALL_PROXY", "socks5://"+listener.Addr().String())
	c, e := telegramProxyDial()(context.Background(), "tcp", "149.154.167.50:443")
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	b := make([]byte, 2)
	if _, e = io.ReadFull(c, b); e != nil || string(b) != "ok" {
		t.Fatal(e, string(b))
	}
	if e = <-done; e != nil {
		t.Fatal(e)
	}
}

func TestTelegramNoProxy(t *testing.T) {
	cleanProxyEnv(t)
	t.Setenv("ALL_PROXY", "http://127.0.0.1:1")
	t.Setenv("NO_PROXY", "*")
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	c, e := telegramProxyDial()(context.Background(), "tcp", l.Addr().String())
	if e != nil {
		t.Fatal(e)
	}
	c.Close()
	for _, raw := range []string{"ftp://example.com", "http://", "http://example.com?secret=foo"} {
		if _, e := parseTelegramProxy(raw); e == nil {
			t.Fatal("accepted invalid proxy")
		}
	}
}
