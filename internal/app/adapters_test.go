package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestAdapterEndpoint(t *testing.T) {
	for _, suffix := range []string{"", "/", "/chat/completions", "/responses/", "/models"} {
		if got := Endpoint("https://example.com/v1"+suffix, "models"); got != "https://example.com/v1/models" {
			t.Fatal(got)
		}
	}
}

func TestChannelLandingPageDiagnostic(t *testing.T) {
	_, err := ParseChannel("<div class=tgme_page_title>频道</div>", "xinjingdaily")
	if err == nil || !strings.Contains(err.Error(), "频道介绍页") {
		t.Fatalf("got %v", err)
	}
	posts, err := ParseChannel("<div class=tgme_page_title>频道</div>"+adapterPostHTML("memes/1", "hello", "", "2026-01-01T00:00:00Z"), "memes")
	if err != nil || len(posts) != 1 {
		t.Fatalf("%v %v", posts, err)
	}
}

func adapterPostHTML(id, text, photos, date string) string {
	return fmt.Sprintf(`<div class="extra tgme_widget_message" data-post="%s"><div class="tgme_widget_message_text">%s</div>%s<time datetime="%s"></time></div>`, id, text, photos, date)
}

func TestAdapterParseChannel(t *testing.T) {
	const date = "2026-01-01T02:00:00+02:00"
	const photos = `<a class="tgme_widget_message_photo_wrap" style="background-image:url('https://cdn4.telegram-cdn.org/a.jpg?a=1&amp;b=2')"></a><a class="tgme_widget_message_photo_wrap" style="background-image:url('https://cdn4.telegram-cdn.org/b.jpg')"></a>`
	source := adapterPostHTML("Memes/42", " hello&amp;<br><b>世界</b>&#33; ", photos, date)
	source += adapterPostHTML("memes/43", "", photos, date)
	for _, id := range []string{"other/1", "memes/../1", "memes/x", "memes/1/2", "memes/1?x"} {
		source += adapterPostHTML(id, "bad", "", date)
	}
	source += adapterPostHTML("memes/44", "bad", "", "2026-02-30T00:00:00Z")
	source += adapterPostHTML("memes/45", "", "", date)
	posts, err := ParseChannel(source, "memes")
	if err != nil || len(posts) != 2 {
		t.Fatalf("%+v %v", posts, err)
	}
	p := posts[0]
	if p.ID != "Memes/42" || p.Channel != "memes" || p.Text != "hello&"+string(rune(10))+"世界!" || len(p.ImageURLs) != 2 || p.ImageURLs[0] != "https://cdn4.telegram-cdn.org/a.jpg?a=1&b=2" || p.CreatedAt != "2026-01-01T00:00:00.000Z" || p.URL != "https://t.me/Memes/42" {
		t.Fatalf("%+v", p)
	}
	if _, err := ParseChannel(source, "me.*s"); err == nil {
		t.Fatal("invalid channel accepted")
	}
	for _, text := range []string{strings.Repeat("中", 12001), strings.Repeat("😀", 6001)} {
		posts, err := ParseChannel(adapterPostHTML("memes/1", text, "", date), "memes")
		if err != nil || len(posts) != 1 {
			t.Fatal(err)
		}
		units := 0
		for _, r := range posts[0].Text {
			units++
			if r > 0xffff {
				units++
			}
		}
		if units != 12000 || !utf8.ValidString(posts[0].Text) {
			t.Fatal("incorrect Unicode limit")
		}
	}
}

func TestAdapterFetchLimited(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" {
			w.WriteHeader(502)
			return
		}
		if r.URL.Path == "/wait" {
			<-r.Context().Done()
			return
		}
		if r.Header.Get("X-Test") != "yes" {
			t.Error("missing header")
		}
		b, _ := io.ReadAll(r.Body)
		if r.URL.Path == "/post" && (r.Method != "POST" || string(b) != "request") {
			t.Error("wrong POST")
		}
		if r.URL.Path == "/get" && r.Method != "GET" {
			t.Error("wrong GET")
		}
		w.Header().Set("Content-Type", "text/plain")
		if r.URL.Path == "/chunked" {
			w.(http.Flusher).Flush()
		}
		io.WriteString(w, "hello")
	}))
	defer server.Close()
	for _, path := range []string{"/get", "/post", "/chunked"} {
		var body []byte
		if path == "/post" {
			body = []byte("request")
		}
		for _, limit := range []int64{0, 4, 5, 6} {
			data, typ, err := FetchLimited(context.Background(), server.Client(), server.URL+path, map[string]string{"X-Test": "yes"}, body, limit)
			if limit < 5 {
				if err == nil {
					t.Fatal("oversized response accepted")
				}
				continue
			}
			if err != nil || string(data) != "hello" || typ != "text/plain" {
				t.Fatalf("%s %s %v", data, typ, err)
			}
		}
	}
	if _, _, err := FetchLimited(context.Background(), server.Client(), server.URL+"/status", nil, nil, 10); err == nil {
		t.Fatal("HTTP error accepted")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, _, err := FetchLimited(ctx, server.Client(), server.URL+"/wait", nil, nil, 10); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline: %v", err)
	}
}

func TestAdapterCallAIProtocols(t *testing.T) {
	picture := previewFixture(t, 1, 1, 0, false)
	for _, mode := range []string{"chat", "responses"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer secret" || r.Header.Get("Content-Type") != "application/json" {
					t.Error("wrong headers or method")
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				if body["model"] != "vision" {
					t.Error("wrong model")
				}
				var content []any
				if mode == "chat" {
					if r.URL.Path != "/v1/chat/completions" {
						t.Error(r.URL.Path)
					}
					messages := body["messages"].([]any)
					if messages[0].(map[string]any)["content"] != "system" {
						t.Error("wrong instruction")
					}
					content = messages[1].(map[string]any)["content"].([]any)
					if content[1].(map[string]any)["type"] != "image_url" || content[1].(map[string]any)["image_url"].(map[string]any)["url"] != picture {
						t.Error("wrong image")
					}
					io.WriteString(w, `{"choices":[{"message":{"content":" ok "}}]}`)
				} else {
					if r.URL.Path != "/v1/responses" || body["instructions"] != "system" {
						t.Error("wrong responses request")
					}
					content = body["input"].([]any)[0].(map[string]any)["content"].([]any)
					if content[1].(map[string]any)["type"] != "input_image" || content[1].(map[string]any)["image_url"] != picture {
						t.Error("wrong image")
					}
					io.WriteString(w, `{"output":[{"content":[{"type":"reasoning","text":"ignored"},{"type":"output_text","text":" ok "}]}]}`)
				}
				if len(content) != 2 || content[0].(map[string]any)["text"] != "text" {
					t.Error("wrong content")
				}
			}))
			defer server.Close()
			got, err := CallAI(context.Background(), server.Client(), AIConfig{BaseURL: server.URL + "/v1/models", Mode: mode, Model: "vision", APIKey: "secret"}, "system", "text", []string{picture})
			if err != nil || got != "ok" {
				t.Fatalf("%q %v", got, err)
			}
		})
	}
}

func TestAdapterAIOutputs(t *testing.T) {
	for _, tc := range []struct{ mode, body, want string }{
		{"responses", `{"output_text":" direct "}`, "direct"},
		{"responses", `{"output":[{"content":[{"type":"output_text","text":"a"},{"type":"output_text","text":"b"}]}]}`, "a" + string(rune(10)) + "b"},
		{"chat", `{"choices":[]}`, ""},
		{"chat", `{"choices":[{"message":{"content":[]}}]}`, ""},
		{"responses", `{"output_text":"   "}`, ""},
		{"responses", `not json`, ""},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, tc.body) }))
		got, err := CallAI(context.Background(), server.Client(), AIConfig{BaseURL: server.URL, Mode: tc.mode, Model: "vision"}, "", "", nil)
		server.Close()
		if got != tc.want || (err != nil) != (tc.want == "") {
			t.Fatalf("%s: %q %v", tc.body, got, err)
		}
	}
}

func TestAdapterParseScore(t *testing.T) {
	for _, input := range []string{`{"score":80.5}`, "```json" + string(rune(10)) + `{"score":80.5}` + string(rune(10)) + "```"} {
		if score, err := ParseScore(input); err != nil || score != 80.5 {
			t.Fatalf("%v %v", score, err)
		}
	}
	for _, input := range []string{`{"score":101}`, `{"score":-1}`, `{"score":null}`, `{"score":"80"}`, `{}`, `null`, `{"score":1e999}`, `{"score":NaN}`, `{"score":50} trailing`} {
		if _, err := ParseScore(input); err == nil {
			t.Fatalf("accepted %s", input)
		}
	}
}
