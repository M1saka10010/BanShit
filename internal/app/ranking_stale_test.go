package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// seedStaleHomepage publishes a full board whose strongest slot holds an item
// that already left the candidate window; the remaining entries are fresh.
func seedStaleHomepage(t *testing.T, a *App, c Config) []Meme {
	t.Helper()
	stale := Meme{ID: "old/stale", Channel: "memes", Text: "long displayed", CreatedAt: "2020-01-01T00:00:00Z"}
	if err := a.storeMeme(stale); err != nil {
		t.Fatal(err)
	}
	home := []Meme{stale}
	for i := 15; i >= 1; i-- {
		m := Meme{ID: fmt.Sprintf("old/%03d", i), Channel: "memes", Text: fmt.Sprintf("in window %d", i), CreatedAt: "2026-02-01T00:00:00Z"}
		if err := a.storeMeme(m); err != nil {
			t.Fatal(err)
		}
		home = append(home, m)
	}
	state := rankingState{Policy: rankingPolicy(c) + ":" + a.meta("rankingGeneration"), Seen: map[string]bool{}}
	for _, m := range home {
		state.Top = append(state.Top, m.ID)
		state.Seen[fingerprintKey(m)] = true
	}
	b, _ := json.Marshal(state)
	if err := a.publishFeed(c, home, "seed", string(b)); err != nil {
		t.Fatal(err)
	}
	if err := a.setMeta("lastRun", "seed time"); err != nil {
		t.Fatal(err)
	}
	a.status.LastRun = "seed time"
	return home
}

func TestStaleHomepageReplacedFirst(t *testing.T) {
	var targets []string
	a, c, _ := admissionApp(t, func(instruction string, rows []rankInput) string {
		if len(rows) != 2 {
			t.Fatalf("expected replacement comparison: %v", rows)
		}
		if rows[1].ID == "old/stale" {
			if !strings.Contains(instruction, "新鲜度已耗尽") || !strings.Contains(instruction, "待替换内容来自原首页：true") {
				t.Errorf("stale target not relaxed: %s", instruction)
			}
		} else if strings.Contains(instruction, "新鲜度已耗尽") {
			t.Errorf("fresh target relaxed: %v", rows)
		}
		if strings.Contains(instruction, "不适用新鲜度优待") {
			t.Errorf("replacement got strict rules: %v", rows)
		}
		targets = append(targets, rows[1].ID)
		return `{"qualified":true,"accept":true,"reason":"cleanup"}`
	})
	home := seedStaleHomepage(t, a, c)
	pool := append([]Meme{}, home[1:]...)
	for _, m := range []Meme{{ID: "new/1", Text: "fresh one"}, {ID: "new/2", Text: "fresh two"}} {
		m.Channel, m.CreatedAt = "memes", "2026-03-01T00:00:00Z"
		if err := a.storeMeme(m); err != nil {
			t.Fatal(err)
		}
		pool = append(pool, m)
	}
	if err := a.rankIncremental(c, pool); err != nil {
		t.Fatal(err)
	}
	// The stale entry must be cleaned up before any fresher homepage item,
	// even though it held the strongest slot.
	if len(targets) < 2 || targets[0] != "old/stale" {
		t.Fatalf("stale content not replaced first: %v", targets)
	}
	got, err := a.publishedCandidates(c)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, m := range got {
		ids[m.ID] = true
	}
	if len(got) != 16 || ids["old/stale"] || !ids["new/1"] || !ids["new/2"] {
		t.Fatalf("feed after cleanup: %v", got)
	}
}

func TestStaleHomepageKeptWithoutCandidates(t *testing.T) {
	a, c, calls := admissionApp(t, func(string, []rankInput) string {
		t.Error("model consulted without fresh candidates")
		return `{"qualified":false,"accept":false,"reason":"unused"}`
	})
	home := seedStaleHomepage(t, a, c)
	if err := a.rankIncremental(c, home[1:]); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("model consulted without fresh candidates")
	}
	got, err := a.publishedCandidates(c)
	if err != nil || len(got) != 16 || got[0].ID != "old/stale" {
		t.Fatalf("stale content evicted without replacement: %v %v", got, err)
	}
	if a.meta("lastRun") != "seed time" {
		t.Fatal("unchanged feed republished")
	}
}
