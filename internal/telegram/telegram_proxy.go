package telegram

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"golang.org/x/net/http/httpproxy"
	"golang.org/x/net/proxy"
)

// Telegram uses raw TCP, so HTTP_PROXY does not apply without a CONNECT tunnel.
// Capture environment once per client; never fall back to direct on proxy errors.
func telegramProxyDial() func(context.Context, string, string) (net.Conn, error) {
	first := func(keys ...string) string {
		for _, k := range keys {
			if v := os.Getenv(k); v != "" {
				return v
			}
		}
		return ""
	}
	raw := first("ALL_PROXY", "all_proxy", "HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy")
	choose := (&httpproxy.Config{HTTPSProxy: raw, NoProxy: first("NO_PROXY", "no_proxy")}).ProxyFunc()
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		u, err := choose(&url.URL{Scheme: "https", Host: address})
		if err != nil {
			return nil, errors.New("Telegram 代理配置无效")
		}
		direct := &net.Dialer{Timeout: 20 * time.Second, KeepAlive: 30 * time.Second}
		if u == nil {
			if raw != "" { // Invalid URLs must not silently become direct connections.
				if _, err := parseTelegramProxy(raw); err != nil {
					return nil, err
				}
			}
			return direct.DialContext(ctx, network, address)
		}
		u, err = parseTelegramProxy(raw)
		if err != nil {
			return nil, err
		}
		if u.Scheme == "socks5" || u.Scheme == "socks5h" {
			var auth *proxy.Auth
			if u.User != nil {
				password, _ := u.User.Password()
				auth = &proxy.Auth{User: u.User.Username(), Password: password}
			}
			d, err := proxy.SOCKS5("tcp", u.Host, auth, direct)
			if err != nil {
				return nil, errors.New("Telegram SOCKS5 代理配置无效")
			}
			conn, err := d.(proxy.ContextDialer).DialContext(ctx, network, address)
			if err != nil {
				return nil, errors.New("Telegram SOCKS5 代理连接失败")
			}
			return conn, nil
		}
		conn, err := direct.DialContext(ctx, "tcp", u.Host)
		if err != nil {
			return nil, errors.New("Telegram HTTP 代理连接失败")
		}
		ok := false
		defer func() {
			if !ok {
				conn.Close()
			}
		}()
		deadline, _ := ctx.Deadline()
		_ = conn.SetDeadline(deadline)
		rawConn := conn
		stop := context.AfterFunc(ctx, func() { rawConn.Close() })
		defer stop()
		if u.Scheme == "https" {
			secure := tls.Client(conn, &tls.Config{ServerName: u.Hostname(), MinVersion: tls.VersionTLS12})
			if err := secure.HandshakeContext(ctx); err != nil {
				return nil, errors.New("Telegram HTTPS 代理握手失败")
			}
			conn = secure
		}
		req := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: address}, Host: address, Header: make(http.Header)}
		if u.User != nil {
			password, _ := u.User.Password()
			req.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(u.User.Username()+":"+password)))
		}
		if err := req.Write(conn); err != nil {
			return nil, errors.New("Telegram 代理 CONNECT 发送失败")
		}
		// Bound the response headers without reading beyond the tunnel boundary.
		header := make([]byte, 0, 1024)
		one := make([]byte, 1)
		for len(header) < 32768 && !bytes.HasSuffix(header, []byte{13, 10, 13, 10}) {
			n, err := conn.Read(one)
			if n > 0 {
				header = append(header, one[0])
			}
			if err != nil {
				return nil, errors.New("Telegram 代理 CONNECT 响应失败")
			}
		}
		if !bytes.HasSuffix(header, []byte{13, 10, 13, 10}) {
			return nil, errors.New("Telegram 代理响应头过大")
		}
		response, err := http.ReadResponse(bufio.NewReader(strings.NewReader(string(header))), req)
		if err != nil || response.StatusCode != http.StatusOK {
			return nil, errors.New("Telegram 代理拒绝 CONNECT，请检查认证和代理规则")
		}
		if !stop() || ctx.Err() != nil {
			return nil, errors.New("Telegram 代理连接已取消")
		}
		_ = conn.SetDeadline(time.Time{})
		ok = true
		return conn, nil
	}
}

func parseTelegramProxy(raw string) (*url.URL, error) {
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("Telegram 代理地址无效")
	}
	switch u.Scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return nil, errors.New("Telegram 代理仅支持 HTTP、HTTPS、SOCKS5")
	}
	if u.Port() == "" {
		port := "1080"
		if u.Scheme == "http" {
			port = "80"
		}
		if u.Scheme == "https" {
			port = "443"
		}
		u.Host = net.JoinHostPort(u.Hostname(), port)
	}
	return u, nil
}
