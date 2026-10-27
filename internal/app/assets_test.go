package app

import (
	"bytes"
	"encoding/json"
	"github.com/M1saka10010/BanShit/public"
	"io/fs"
	"testing"
)

func TestStaticAssets(t *testing.T) {
	a := &App{}
	cases := []struct{ path, file, mime string }{
		{"/", "index.html", "text/html; charset=utf-8"},
		{"/admin", "admin.html", "text/html; charset=utf-8"},
		{"/admin/", "admin.html", "text/html; charset=utf-8"},
		{"/favicon.svg", "favicon.svg", "image/svg+xml"},
	}
	for _, group := range []struct{ pattern, mime string }{
		{"js/lib/*.js", "text/javascript; charset=utf-8"},
		{"css/*.css", "text/css; charset=utf-8"},
	} {
		matches, err := fs.Glob(public.Assets, group.pattern)
		if err != nil || len(matches) == 0 {
			t.Fatalf("asset pattern %s: matches=%v err=%v", group.pattern, matches, err)
		}
		for _, match := range matches {
			file := match
			cases = append(cases, struct{ path, file, mime string }{"/" + file, file, group.mime})
		}
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			w := req(a, "GET", tc.path, "")
			checkCode(t, w, 200)
			if got := w.Header().Get("Content-Type"); got != tc.mime {
				t.Fatalf("Content-Type = %q, want %q", got, tc.mime)
			}
			want, err := public.Assets.ReadFile(tc.file)
			if err != nil {
				t.Fatal(err)
			}
			if len(want) == 0 || !bytes.Equal(w.Body.Bytes(), want) {
				t.Fatal("response does not match embedded asset")
			}
		})
	}
}

func TestStaticAssetsNotFound(t *testing.T) {
	a := &App{}
	for _, path := range []string{"/js/lib/missing.js", "/css/missing.css", "/missing.svg", "/js/lib/../missing.js"} {
		t.Run(path, func(t *testing.T) {
			w := req(a, "GET", path, "")
			checkCode(t, w, 404)
			if got := w.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
				t.Fatalf("404 Content-Type = %q", got)
			}
			var body map[string]string
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body["error"] != "未找到" {
				t.Fatalf("unexpected error: %v", body)
			}
		})
	}
}
