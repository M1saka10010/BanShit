package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAIRetryProtocols(t *testing.T) {
	for _, mode := range []string{"chat", "responses"} {
		for _, status := range []int{429, 502, 503, 504} {
			for _, succeed := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%d/%v", mode, status, succeed), func(t *testing.T) {
					attempts := 0
					var first string
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						attempts++
						body, _ := io.ReadAll(r.Body)
						if attempts == 1 {
							first = string(body)
						} else if string(body) != first {
							t.Error("body changed")
						}
						if r.Header.Get("Authorization") != "Bearer secret" {
							t.Error("missing authorization")
						}
						path := "/responses"
						if mode == "chat" {
							path = "/chat/completions"
						}
						if r.URL.Path != path {
							t.Error(r.URL.Path)
						}
						if succeed && attempts == 4 {
							io.WriteString(w, `{"output_text":"ok","choices":[{"message":{"content":"ok"}}]}`)
							return
						}
						w.WriteHeader(status)
						io.WriteString(w, "secret response")
					}))
					defer server.Close()
					var delays []time.Duration
					wait := func(_ context.Context, d time.Duration) error { delays = append(delays, d); return nil }
					output, err := callAI(context.Background(), server.Client(), AIConfig{Mode: mode, Model: "m", BaseURL: server.URL, APIKey: "secret"}, "i", "t", []string{previewFixture(t, 1, 1, 0, false)}, aiRequestTimeout, wait)
					if attempts != 4 || !reflect.DeepEqual(delays, []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}) {
						t.Fatalf("attempts=%d delays=%v", attempts, delays)
					}
					if succeed {
						if err != nil || output != "ok" {
							t.Fatalf("%q %v", output, err)
						}
					} else if err == nil || !strings.Contains(err.Error(), "已尝试 4 次") || strings.Contains(err.Error(), "secret") {
						t.Fatal(err)
					}
				})
			}
		}
	}
}

func TestAINoRetry(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		length string
	}{
		{"400", 400, "secret", ""}, {"401", 401, "secret", ""}, {"403", 403, "secret", ""}, {"413", 413, "secret", ""}, {"500", 500, "secret", ""},
		{"json", 200, `{"output_text":{"secret":true}}`, ""}, {"empty", 200, `{}`, ""},
		{"large-length", 200, "", "8388609"}, {"large-stream", 200, strings.Repeat("x", 8388609), ""},
		{"invalid-score", 200, `{"output_text":"not a score"}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			attempts := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempts++
				if tc.length != "" {
					w.Header().Set("Content-Length", tc.length)
				}
				w.WriteHeader(tc.status)
				if tc.name == "large-stream" {
					w.(http.Flusher).Flush()
				}
				io.WriteString(w, tc.body)
			}))
			defer server.Close()
			output, err := callAI(context.Background(), server.Client(), AIConfig{Mode: "responses", Model: "m", BaseURL: server.URL}, "", "", nil, aiRequestTimeout, func(context.Context, time.Duration) error { t.Error("unexpected retry"); return nil })
			if tc.name == "invalid-score" {
				if err != nil {
					t.Fatal(err)
				}
				_, err = ParseScore(output)
			}
			if attempts != 1 || err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatalf("%d %v", attempts, err)
			}
		})
	}
}

func TestAIRetryAfter(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, tc := range []struct {
		value string
		delay time.Duration
		stop  bool
	}{
		{"12", 12 * time.Second, false}, {now.Add(20 * time.Second).Format(http.TimeFormat), 20 * time.Second, false},
		{"301", 0, true}, {"99999999999999999999999999", 0, true}, {now.Add(time.Hour).Format(http.TimeFormat), 0, true},
		{"bad", 0, false}, {"-1", 0, false}, {now.Add(-time.Hour).Format(http.TimeFormat), 0, false},
	} {
		d, stop := aiRetryAfter(tc.value, now)
		if d != tc.delay || stop != tc.stop {
			t.Fatalf("%s: %v %v", tc.value, d, stop)
		}
	}
	for _, value := range []string{"12", time.Now().Add(20 * time.Second).UTC().Format(http.TimeFormat), "301", "99999999999999999999999999"} {
		attempts := 0
		waits := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attempts++
			w.Header().Set("Retry-After", value)
			w.WriteHeader(429)
		}))
		_, _, err := fetchAI(context.Background(), server.Client(), server.URL, nil, []byte(`{}`), aiRequestTimeout, func(_ context.Context, d time.Duration) error {
			waits++
			if d < 10*time.Second {
				t.Error(d)
			}
			return nil
		})
		server.Close()
		if err == nil {
			t.Fatal("expected error")
		}
		if value == "301" || strings.HasPrefix(value, "999") {
			if attempts != 1 || waits != 0 || !strings.Contains(err.Error(), "避免提前请求") {
				t.Fatalf("%d %d %v", attempts, waits, err)
			}
		} else if attempts != 4 || waits != 3 {
			t.Fatalf("%d %d", attempts, waits)
		}
	}
}

type aiRoundTripFunc func(*http.Request) (*http.Response, error)

func (f aiRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type aiTemporaryError struct{}

func (aiTemporaryError) Error() string   { return "secret network details" }
func (aiTemporaryError) Timeout() bool   { return false }
func (aiTemporaryError) Temporary() bool { return true }

type aiTrackedBody struct {
	io.Reader
	closed *int
}

func (b aiTrackedBody) Close() error { *b.closed++; return nil }

func TestAIClosesEveryResponse(t *testing.T) {
	for _, status := range []int{200, 400, 503} {
		closed := 0
		client := &http.Client{Transport: aiRoundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status, Header: make(http.Header), Body: aiTrackedBody{strings.NewReader(`{}`), &closed}}, nil
		})}
		_, attempts, _ := fetchAI(context.Background(), client, "http://example.test", nil, []byte(`{}`), aiRequestTimeout, func(context.Context, time.Duration) error { return nil })
		if closed != attempts {
			t.Fatalf("status=%d closed=%d attempts=%d", status, closed, attempts)
		}
	}
}

func TestAITimeoutAndClientIsolation(t *testing.T) {
	client := &http.Client{Timeout: 90 * time.Second, Transport: aiRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		remaining := time.Until(deadline)
		if !ok || remaining < 299*time.Second || remaining > 300*time.Second {
			t.Errorf("deadline %v", remaining)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"output_text":"ok"}`)), Header: make(http.Header)}, nil
	})}
	if _, err := CallAI(context.Background(), client, AIConfig{Mode: "responses", Model: "m", BaseURL: "http://example.test"}, "", "", nil); err != nil {
		t.Fatal(err)
	}
	if client.Timeout != 90*time.Second {
		t.Fatal("client mutated")
	}
	client.Transport = aiRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		deadline, _ := r.Context().Deadline()
		if d := time.Until(deadline); d < 89*time.Second || d > 90*time.Second {
			t.Error(d)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ok")), Header: make(http.Header)}, nil
	})
	if _, _, err := FetchLimited(context.Background(), client, "http://example.test", nil, nil, 10); err != nil {
		t.Fatal(err)
	}
}

func TestAINetworkAndCancellation(t *testing.T) {
	for _, kind := range []string{"timeout", "temporary", "permanent", "cancel-request", "cancel-wait", "canceled"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			attempts := 0
			waits := 0
			if kind == "canceled" {
				cancel()
			}
			client := &http.Client{Transport: aiRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				attempts++
				switch kind {
				case "timeout":
					<-r.Context().Done()
					return nil, r.Context().Err()
				case "permanent":
					return nil, errors.New("secret")
				case "cancel-request":
					cancel()
					return nil, aiTemporaryError{}
				default:
					return nil, aiTemporaryError{}
				}
			})}
			_, _, err := fetchAI(ctx, client, "http://example.test", nil, []byte(`{}`), time.Millisecond, func(ctx context.Context, d time.Duration) error {
				waits++
				if kind == "cancel-wait" {
					cancel()
					return waitAIRetry(ctx, time.Hour)
				}
				return nil
			})
			want := 4
			if kind == "permanent" || strings.HasPrefix(kind, "cancel-") {
				want = 1
			}
			if kind == "canceled" {
				want = 0
			}
			if attempts != want || err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatalf("%d %d %v", attempts, waits, err)
			}
		})
	}
}

func TestAIHTTPTimeout(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		attempts.Add(1)
		<-r.Context().Done()
	}))
	defer server.Close()
	_, n, err := fetchAI(context.Background(), server.Client(), server.URL, nil, []byte(`{}`), 20*time.Millisecond, func(context.Context, time.Duration) error { return nil })
	if err == nil || n != 4 || attempts.Load() != 4 {
		t.Fatalf("%d %d %v", n, attempts.Load(), err)
	}
}
