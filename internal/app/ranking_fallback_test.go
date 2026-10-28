package app

import (
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
)

func TestIncrementalContradictionFallsBack(t *testing.T) {
	for _, failSerial := range []bool{false, true} {
		t.Run(fmt.Sprint(failSerial), func(t *testing.T) {
			var calls atomic.Int32
			model, mc := rankingModel(t, func(items []rankInput) string {
				call := calls.Add(1)
				if failSerial && call > 2 {
					return "{}"
				}
				// First parallel pivots are old/011 and old/006. Claim the new
				// item beats the higher one but loses to the lower one.
				if items[1].ID == "old/006" {
					items[0], items[1] = items[1], items[0]
				}
				rows := []map[string]string{}
				for _, m := range items {
					rows = append(rows, map[string]string{"id": m.ID, "reason": "comparison"})
				}
				b, _ := json.Marshal(map[string]any{"ranking": rows})
				return string(b)
			}, nil, func(string, []rankInput) string {
				b, _ := json.Marshal(map[string]any{"qualified": true, "accept": true, "reason": "qualified"})
				return string(b)
			})
			a := testApp(t)
			c := a.snapshot()
			c.AI = mc.AI
			c.AI.Concurrency = 2
			c.Channels = []string{"memes"}
			a.config, a.client = c, model.client
			old := seedAdmission(t, a, c)
			before := a.meta("rankingState")
			fresh := Meme{ID: "new/fallback", Channel: "memes", Text: "fresh fallback candidate"}
			if err := a.storeMeme(fresh); err != nil {
				t.Fatal(err)
			}
			err := a.rankIncremental(c, []Meme{fresh})
			if (err != nil) != failSerial {
				t.Fatalf("unexpected error: %v", err)
			}
			got, err := a.publishedCandidates(c)
			if err != nil || len(got) != 16 {
				t.Fatalf("feed size=%d err=%v", len(got), err)
			}
			if failSerial {
				if calls.Load() != 3 {
					t.Fatalf("calls=%d", calls.Load())
				}
				for i := range old {
					if got[i].ID != old[i].ID {
						t.Fatal("partial publication")
					}
				}
				if a.meta("rankingState") != before || a.meta("lastRun") != "seed time" {
					t.Fatal("failed fallback persisted")
				}
			} else {
				// Two conflicting comparisons plus four bounded serial comparisons.
				if calls.Load() != 6 || got[0].ID != fresh.ID {
					t.Fatalf("fallback calls=%d first=%s", calls.Load(), got[0].ID)
				}
				seen := map[string]bool{}
				for _, m := range got {
					if seen[m.ID] {
						t.Fatal("duplicate publication")
					}
					seen[m.ID] = true
				}
				if err := a.rankIncremental(c, []Meme{fresh}); err != nil {
					t.Fatal(err)
				}
				if calls.Load() != 6 {
					t.Fatal("published candidate reevaluated")
				}
			}
		})
	}
}

func TestIncrementalRepeatedContradictions(t *testing.T) {
	var calls atomic.Int32
	model, mc := rankingModel(t, func(items []rankInput) string {
		calls.Add(1)
		// Each candidate's parallel pivots are top[5] and top[10]; losing to
		// the lower pivot while beating the upper one is a contradiction.
		if items[1].ID == "old/006" || items[1].ID == "old/007" {
			items[0], items[1] = items[1], items[0]
		}
		rows := []map[string]string{}
		for _, m := range items {
			rows = append(rows, map[string]string{"id": m.ID, "reason": "comparison"})
		}
		b, _ := json.Marshal(map[string]any{"ranking": rows})
		return string(b)
	}, nil, func(string, []rankInput) string {
		b, _ := json.Marshal(map[string]any{"qualified": true, "accept": true, "reason": "qualified"})
		return string(b)
	})
	a := testApp(t)
	c := a.snapshot()
	c.AI = mc.AI
	c.AI.Concurrency = 2
	c.Channels = []string{"memes"}
	a.config, a.client = c, model.client
	seedAdmission(t, a, c)
	pool := []Meme{
		{ID: "new/a", Channel: "memes", Text: "first fresh candidate"},
		{ID: "new/b", Channel: "memes", Text: "second fresh candidate"},
	}
	for _, m := range pool {
		if err := a.storeMeme(m); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.rankIncremental(c, pool); err != nil {
		t.Fatal(err)
	}
	got, err := a.publishedCandidates(c)
	if err != nil || len(got) != 16 {
		t.Fatalf("feed size=%d err=%v", len(got), err)
	}
	// Both candidates contradict, fall back to serial, and still publish.
	if calls.Load() != 12 || got[0].ID != "new/b" || got[1].ID != "new/a" {
		t.Fatalf("calls=%d first=%s second=%s", calls.Load(), got[0].ID, got[1].ID)
	}
	seen := map[string]bool{}
	for _, m := range got {
		if seen[m.ID] {
			t.Fatal("duplicate publication")
		}
		seen[m.ID] = true
	}
}
