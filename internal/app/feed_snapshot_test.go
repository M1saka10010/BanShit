package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestFeedConcurrentSnapshot(t *testing.T) {
	a := testApp(t)
	for _, id := range []string{"first", "second"} {
		if err := a.storeMeme(Meme{ID: id, Channel: id, Text: id}); err != nil {
			t.Fatal(err)
		}
	}
	// Distinct generation markers avoid the publisher's second-resolution clock.
	publish := func(id string) error {
		a.mu.Lock()
		defer a.mu.Unlock()
		if _, err := a.db.Exec("INSERT OR REPLACE INTO feed(position,memeId) VALUES(1,?)", id); err != nil {
			return err
		}
		a.config.Channels = []string{id}
		a.status.LastRun = id
		return nil
	}
	if err := publish("first"); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 500; i++ {
			if err := publish([]string{"first", "second"}[i%2]); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	for reader := 0; reader < 4; reader++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < 250; i++ {
				w := httptest.NewRecorder()
				a.feed(w, httptest.NewRequest(http.MethodGet, "/api/memes", nil))
				var data struct {
					Memes     []Meme
					UpdatedAt string
				}
				if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
					t.Error(err)
					return
				}
				if w.Code != http.StatusOK || len(data.Memes) != 1 || data.Memes[0].ID != data.UpdatedAt {
					t.Errorf("inconsistent snapshot: status=%d body=%s", w.Code, w.Body.String())
					return
				}
			}
		}()
	}
	close(start)
	wg.Wait()
}

type feedUnlockedWriter struct {
	*httptest.ResponseRecorder
	a *App
	t *testing.T
}

func (w *feedUnlockedWriter) checkUnlocked() {
	w.t.Helper()
	if !w.a.mu.TryLock() {
		w.t.Error("feed holds app mutex while writing response")
		return
	}
	w.a.mu.Unlock()
}

func (w *feedUnlockedWriter) WriteHeader(code int) {
	w.checkUnlocked()
	w.ResponseRecorder.WriteHeader(code)
}

func (w *feedUnlockedWriter) Write(body []byte) (int, error) {
	w.checkUnlocked()
	return w.ResponseRecorder.Write(body)
}

func TestFeedResponseReleasesSnapshotLock(t *testing.T) {
	for _, scenario := range []string{"success", "empty channels", "query error", "scan error", "image error"} {
		t.Run(scenario, func(t *testing.T) {
			a := testApp(t)
			m := Meme{ID: "memes/1", Channel: "memes", Text: "test"}
			if err := a.storeMeme(m); err != nil {
				t.Fatal(err)
			}
			if err := a.publishFeed(a.snapshot(), []Meme{m}); err != nil {
				t.Fatal(err)
			}
			want := http.StatusOK
			switch scenario {
			case "empty channels":
				a.config.Channels = nil
			case "query error":
				if _, err := a.db.Exec("DROP TABLE feed"); err != nil {
					t.Fatal(err)
				}
				want = http.StatusInternalServerError
			case "scan error", "image error":
				images := `[123]`
				if scenario == "image error" {
					images = `["invalid-ref"]`
				}
				if _, err := a.db.Exec("UPDATE memes SET images=?", images); err != nil {
					t.Fatal(err)
				}
				want = http.StatusInternalServerError
			}
			w := &feedUnlockedWriter{ResponseRecorder: httptest.NewRecorder(), a: a, t: t}
			a.feed(w, httptest.NewRequest(http.MethodGet, "/api/memes", nil))
			checkCode(t, w.ResponseRecorder, want)
		})
	}
}
