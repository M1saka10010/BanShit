package app

import (
	"encoding/json"
	"testing"
)

func TestFeedAtomicPublication(t *testing.T) {
	a := testApp(t)
	c := a.snapshot()
	old := Meme{ID: "memes/1", Text: "old", Channel: "memes"}
	fresh := Meme{ID: "memes/2", Text: "fresh", Channel: "memes"}
	for _, m := range []Meme{old, fresh} {
		if e := a.storeMeme(m); e != nil {
			t.Fatal(e)
		}
	}
	if e := a.publishFeed(c, []Meme{old}); e != nil {
		t.Fatal(e)
	}
	assertFeed := func(ids ...string) {
		t.Helper()
		w := req(a, "GET", "/api/memes", "")
		checkCode(t, w, 200)
		var data struct{ Memes []Meme }
		if e := json.Unmarshal(w.Body.Bytes(), &data); e != nil {
			t.Fatal(e)
		}
		if len(data.Memes) != len(ids) {
			t.Fatal(data)
		}
		for i, id := range ids {
			if data.Memes[i].ID != id {
				t.Fatal(data)
			}
		}
	}
	assertFeed(old.ID)
	if e := a.publishFeed(c, []Meme{fresh, fresh}); e == nil {
		t.Fatal("duplicate accepted")
	}
	assertFeed(old.ID)
	if e := a.publishFeed(c, []Meme{{ID: "missing"}}); e == nil {
		t.Fatal("unknown accepted")
	}
	assertFeed(old.ID)
	stale := c
	stale.InterestPrompt = "stale"
	if e := a.publishFeed(stale, []Meme{fresh}); e == nil {
		t.Fatal("stale config accepted")
	}
	assertFeed(old.ID)
	if e := a.publishFeed(c, []Meme{fresh, old}); e != nil {
		t.Fatal(e)
	}
	assertFeed(fresh.ID, old.ID)
	if e := migrateFeed(a.db); e != nil {
		t.Fatal(e)
	}
	assertFeed(fresh.ID, old.ID)
}

func TestCanceledPublicationPreservesFeed(t *testing.T) {
	a := testApp(t)
	c := a.snapshot()
	old := Meme{ID: "memes/1", Text: "old", Channel: "memes"}
	if err := a.storeMeme(old); err != nil {
		t.Fatal(err)
	}
	if err := a.publishFeed(c, []Meme{old}, "old identity", "old state"); err != nil {
		t.Fatal(err)
	}
	before := a.meta("lastRun")
	a.cancel()
	if err := a.publishFeed(c, nil, "new identity", "new state"); err == nil {
		t.Fatal("canceled task published")
	}
	got, err := a.publishedCandidates(c)
	if err != nil || len(got) != 1 || got[0].ID != old.ID {
		t.Fatalf("feed changed: %v %v", got, err)
	}
	if a.meta("rankingIdentity") != "old identity" || a.meta("rankingState") != "old state" || a.meta("lastRun") != before {
		t.Fatal("canceled task changed metadata")
	}
}
