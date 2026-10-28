package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMediaFilesAndAccess(t *testing.T) {
	a := testApp(t)
	pics := []string{previewFixture(t, 800, 400, 1, false), previewFixture(t, 40, 80, 2, true)}
	if err := a.storeMeme(Meme{ID: "memes/100", Images: pics, Channel: "memes"}); err != nil {
		t.Fatal(err)
	}
	m, err := scanMeme(a.db.QueryRow("SELECT * FROM memes WHERE id='memes/100'"))
	if err != nil {
		t.Fatal(err)
	}
	urls, thumbs, err := publicImages(m.Images)
	if err != nil {
		t.Fatal(err)
	}
	publicMeme := m
	publicMeme.Images, publicMeme.Thumbnails = urls, thumbs
	encodedMeme, _ := json.Marshal(publicMeme)
	if len(encodedMeme) > 1000 || strings.Contains(string(encodedMeme), "base64") {
		t.Fatal("large image leaked into JSON")
	}
	if len(urls) != 2 {
		t.Fatal(urls)
	}
	for i, url := range urls {
		w := req(a, "GET", url, "")
		checkCode(t, w, 200)
		head := req(a, "HEAD", url, "")
		checkCode(t, head, 200)
		if head.Body.Len() != 0 {
			t.Fatal("HEAD has body")
		}
		_, encoded, _ := strings.Cut(pics[i], ",")
		original, _ := base64.StdEncoding.DecodeString(encoded)
		if !bytes.Equal(w.Body.Bytes(), original) {
			t.Fatal("original changed")
		}
		r := httptest.NewRequest("GET", url, nil)
		r.Header.Set("If-None-Match", w.Header().Get("ETag"))
		cached := httptest.NewRecorder()
		a.ServeHTTP(cached, r)
		checkCode(t, cached, 304)
		if cached.Header().Get("Cache-Control") != "private, no-cache" {
			t.Fatal(cached.Header())
		}
		thumb := req(a, "GET", thumbs[i], "")
		checkCode(t, thumb, 200)
		cfg, _, err := image.DecodeConfig(thumb.Body)
		if err != nil || max(cfg.Width, cfg.Height) > 640 {
			t.Fatal(cfg, err)
		}
		resolved, err := resolveAIImage(a.aiContext(context.Background()), m.Images[i])
		if err != nil || resolved != pics[i] {
			t.Fatal(err)
		}
	}
	session := login(t, a)
	a.mu.Lock()
	a.config.Channels = nil
	a.mu.Unlock()
	r := httptest.NewRequest("GET", urls[0], nil)
	r.Header.Set("If-None-Match", "*")
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	checkCode(t, w, 404)
	checkCode(t, req(a, "GET", urls[0], "", session), 200)
	for _, path := range []string{"/media/../config.json", "/media/" + strings.Repeat("a", 64) + ".png", "/media/" + strings.Repeat("a", 64) + "/png"} {
		checkCode(t, req(a, "GET", path, "", session), 404)
	}
	if _, err := a.db.Exec("DELETE FROM memes WHERE id='memes/100'"); err != nil {
		t.Fatal(err)
	}
	checkCode(t, req(a, "GET", urls[0], "", session), 404)
}

func TestMediaMigrationFailureAndRetry(t *testing.T) {
	a := testApp(t)
	good := previewFixture(t, 8, 8, 1, false)
	raw, _ := json.Marshal([]string{good, "data:image/png;base64,YQ=="})
	if _, err := a.db.Exec("DELETE FROM meta WHERE key='mediaFilesV1'; INSERT INTO memes VALUES('memes/1','',?,'memes','',0,'')", string(raw)); err != nil {
		t.Fatal(err)
	}
	if err := a.media.migrate(a.db); err == nil {
		t.Fatal("invalid image accepted")
	}
	var after string
	a.db.QueryRow("SELECT images FROM memes").Scan(&after)
	if after != string(raw) {
		t.Fatal("failed migration modified row")
	}
	raw, _ = json.Marshal([]string{good, good})
	a.db.Exec("UPDATE memes SET images=?", string(raw))
	for i := 0; i < 2; i++ {
		if err := a.media.migrate(a.db); err != nil {
			t.Fatal(err)
		}
	}
	a.db.QueryRow("SELECT images FROM memes").Scan(&after)
	refs, err := decodeImages(after)
	if err != nil || len(refs) != 2 || refs[0] != refs[1] || !mediaRefPattern.MatchString(refs[0]) {
		t.Fatal(after, err)
	}
	entries, err := os.ReadDir(a.media.dir)
	if err != nil || len(entries) != 2 {
		t.Fatal(entries, err)
	}
	if err = os.Remove(filepath.Join(a.media.dir, strings.TrimPrefix(refs[0], "media:"))); err != nil {
		t.Fatal(err)
	}
	if err = a.media.migrate(a.db); err == nil {
		t.Fatal("missing original ignored after migration marker")
	}
}

func TestMediaRejectUnsafeInput(t *testing.T) {
	s := &mediaStore{dir: t.TempDir()}
	for _, value := range []string{"http://127.0.0.1/private", "media:../private", "data:image/png;base64," + strings.Repeat("A", base64.StdEncoding.EncodedLen(mediaMaxBytes)+4)} {
		if _, err := s.storeImages([]string{value}); err == nil {
			t.Fatal("unsafe accepted")
		}
	}
	ref := "media:" + strings.Repeat("a", 64) + ".png"
	if err := os.Symlink(filepath.Join(t.TempDir(), "outside"), filepath.Join(s.dir, strings.TrimPrefix(ref, "media:"))); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.read(ref); err == nil {
		t.Fatal("symlink escaped")
	}
}

func TestMediaMigrationPartialResume(t *testing.T) {
	a := testApp(t)
	good, _ := json.Marshal([]string{previewFixture(t, 8, 8, 1, false)})
	bad := `["data:image/png;base64,YQ=="]`
	a.db.Exec("DELETE FROM meta WHERE key='mediaFilesV1'")
	for i, raw := range []string{string(good), bad} {
		if _, err := a.db.Exec("INSERT INTO memes VALUES(?,'',?,'memes','',0,'')", string(rune('a'+i)), raw); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.media.migrate(a.db); err == nil || !strings.Contains(err.Error(), `id="b"`) {
		t.Fatal(err)
	}
	var first, second string
	a.db.QueryRow("SELECT images FROM memes WHERE id='a'").Scan(&first)
	a.db.QueryRow("SELECT images FROM memes WHERE id='b'").Scan(&second)
	if !strings.Contains(first, "media:") || second != bad {
		t.Fatal("partial migration corrupted rows")
	}
	a.db.Exec("UPDATE memes SET images=? WHERE id='b'", string(good))
	if err := a.media.migrate(a.db); err != nil {
		t.Fatal(err)
	}
	var next string
	a.db.QueryRow("SELECT images FROM memes WHERE id='a'").Scan(&next)
	if next != first {
		t.Fatal("resume changed reference")
	}
}
