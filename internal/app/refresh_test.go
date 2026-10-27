package app

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type refreshTransport func(*http.Request) (*http.Response, error)

func (f refreshTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestRefreshIsolation(t *testing.T) {
	const photo = `<a class="tgme_widget_message_photo_wrap" style="background-image:url('https://cdn4.telegram-cdn.org/a.jpg')"></a>`
	const date = "2026-01-01T00:00:00Z"
	for _, tc := range []struct {
		name       string
		channels   []string
		body       string
		cached     bool
		warning    string
		want       string
		rankCalls  int
		badRanking bool
	}{
		{name: "partial success", channels: []string{"broken", "memes"}, body: adapterPostHTML("memes/2", "fresh", "", date), cached: true, warning: "broken:", want: "memes/2,memes/1", rankCalls: 1},
		{name: "existing candidates", channels: []string{"broken", "memes"}, body: adapterPostHTML("memes/1", "cached", "", date), cached: true, warning: "broken:", want: "memes/1"},
		{name: "all failed with cache", channels: []string{"broken", "empty"}, cached: true, warning: "broken:", want: "old/1"},
		{name: "all failed without cache", channels: []string{"broken", "empty"}, warning: "empty:", want: "old/1"},
		{name: "image failure isolated", channels: []string{"memes"}, body: adapterPostHTML("memes/3", "bad image", photo, date) + adapterPostHTML("memes/2", "fresh", "", date), cached: true, warning: "memes/3:", want: "memes/2,memes/1", rankCalls: 1},
		{name: "success without valid candidates", channels: []string{"memes"}, body: adapterPostHTML("memes/3", "bad image", photo, date), warning: "memes/3:", want: "old/1"},
		{name: "zero channels", channels: []string{}, want: ""},
		{name: "ranking failure keeps warning", channels: []string{"broken", "memes"}, body: adapterPostHTML("memes/2", "fresh", "", date), cached: true, warning: "broken:", want: "old/1", rankCalls: 1, badRanking: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			var reply func([]rankInput) string
			if tc.badRanking {
				reply = func([]rankInput) string { return `{}` }
			}
			model, config := rankingModel(t, reply, func(_ string, _ []rankInput, _ []string) { calls++ })
			a := testApp(t)
			c := a.snapshot()
			c.AI = config.AI
			c.Channels = tc.channels
			a.config = c
			a.client = &http.Client{Transport: refreshTransport(func(r *http.Request) (*http.Response, error) {
				if strings.HasPrefix(r.URL.String(), config.AI.BaseURL+"/") {
					return model.client.Transport.RoundTrip(r)
				}
				w := httptest.NewRecorder()
				switch r.URL.Host + r.URL.Path {
				case "t.me/s/memes":
					fmt.Fprint(w, tc.body)
				case "t.me/s/empty":
					fmt.Fprint(w, "no posts")
				case "t.me/s/broken", "cdn4.telegram-cdn.org/a.jpg":
					w.WriteHeader(http.StatusBadGateway)
				default:
					return nil, fmt.Errorf("unexpected request: %s", r.URL)
				}
				return w.Result(), nil
			})}
			old := Meme{ID: "old/1", Channel: "old", Text: "old homepage"}
			if err := a.storeMeme(old); err != nil {
				t.Fatal(err)
			}
			if tc.cached {
				channel := "memes"
				if strings.HasPrefix(tc.name, "all failed") {
					channel = "broken"
				}
				if err := a.storeMeme(Meme{ID: "memes/1", Channel: channel, Text: "cached"}); err != nil {
					t.Fatal(err)
				}
			}
			if err := a.publishFeed(c, []Meme{old}, "old identity"); err != nil {
				t.Fatal(err)
			}
			if err := a.setMeta("lastRun", "old run"); err != nil {
				t.Fatal(err)
			}
			a.status.LastRun = "old run"
			if !a.launch(false) {
				t.Fatal("launch rejected")
			}
			a.wg.Wait()
			if tc.warning != "" {
				if !strings.Contains(a.status.LastError, tc.warning) {
					t.Fatalf("warning lost: %q", a.status.LastError)
				}
			} else if a.status.LastError != "" {
				t.Fatal(a.status.LastError)
			}
			if tc.badRanking && !strings.Contains(a.status.LastError, "批量排序失败") {
				t.Fatal(a.status.LastError)
			}
			if calls != tc.rankCalls {
				t.Fatalf("ranking calls = %d, want %d", calls, tc.rankCalls)
			}
			rows, err := a.db.Query("SELECT memeId FROM feed ORDER BY position")
			if err != nil {
				t.Fatal(err)
			}
			var ids []string
			for rows.Next() {
				var id string
				if err := rows.Scan(&id); err != nil {
					t.Fatal(err)
				}
				ids = append(ids, id)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(ids, ",") != tc.want {
				t.Fatalf("feed = %v, want %s", ids, tc.want)
			}
			if tc.want == "old/1" {
				if a.meta("rankingIdentity") != "old identity" || a.meta("lastRun") != "old run" || a.status.LastRun != "old run" {
					t.Fatal("publication metadata changed")
				}
			} else if a.meta("rankingIdentity") == "old identity" || a.meta("lastRun") == "old run" {
				t.Fatal("publication metadata not updated")
			}
			var failedImages int
			if err := a.db.QueryRow("SELECT count(*) FROM memes WHERE id='memes/3'").Scan(&failedImages); err != nil || failedImages != 0 {
				t.Fatalf("failed image stored: %d, %v", failedImages, err)
			}
		})
	}
}
