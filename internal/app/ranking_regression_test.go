package app

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestRankingContentIdentity(t *testing.T) {
	for _, edit := range []bool{false, true} {
		t.Run(fmt.Sprint(edit), func(t *testing.T) {
			reviews := 0
			a, c, _ := admissionApp(t, func(_ string, rows []rankInput) string {
				reviews++
				return `{"qualified":false,"accept":false,"reason":"poor"}`
			})
			old := seedAdmission(t, a, c)
			if err := a.rankIncremental(c, nil); err != nil {
				t.Fatal(err)
			}
			m := old[0]
			if edit {
				m.Text = "changed content"
			} else {
				m.ID = "duplicate"
			}
			if err := a.storeMeme(m); err != nil {
				t.Fatal(err)
			}
			if err := a.rankIncremental(c, []Meme{m}); err != nil {
				t.Fatal(err)
			}
			got, _ := a.publishedCandidates(c)
			if edit {
				if reviews != 1 || len(got) != 15 {
					t.Fatalf("edited content bypassed admission: reviews=%d len=%d", reviews, len(got))
				}
			} else if reviews != 0 || len(got) != 16 || got[0].ID != old[0].ID {
				t.Fatalf("duplicate displaced original: reviews=%d feed=%v", reviews, got)
			}
		})
	}
}

func TestRankingColdStartPromptAndRetryNoop(t *testing.T) {
	reviews := 0
	a, c, _ := admissionApp(t, func(instruction string, _ []rankInput) string {
		reviews++
		if !strings.Contains(instruction, "不适用约4条换新目标或新鲜度优待") {
			t.Error("cold start granted freshness preference")
		}
		return `{"qualified":false,"accept":false,"reason":"poor"}`
	})
	m := Meme{ID: "new", Channel: "memes", Text: "poor"}
	if err := a.storeMeme(m); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := a.rankIncremental(c, []Meme{m}); err != nil {
			t.Fatal(err)
		}
	}
	if reviews != 1 {
		t.Fatalf("unchanged rejection retried %d times", reviews)
	}
}

func TestRankingRejectDoesNotGetSecondAdmission(t *testing.T) {
	reviews := 0
	a, c, _ := admissionApp(t, func(_ string, _ []rankInput) string {
		reviews++
		return fmt.Sprintf(`{"qualified":%t,"accept":%t,"reason":"quality"}`, reviews != 2, reviews != 2)
	})
	seedAdmission(t, a, c)
	pool := []Meme{{ID: "new/1", Channel: "memes", Text: "one"}, {ID: "new/2", Channel: "memes", Text: "two"}}
	for _, m := range pool {
		if err := a.storeMeme(m); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.rankIncremental(c, pool); err != nil {
		t.Fatal(err)
	}
	if reviews != 2 {
		t.Fatalf("rejection overturned by repeated admission: %d", reviews)
	}
	got, _ := a.publishedCandidates(c)
	if got[15].ID != "new/1" {
		t.Fatalf("rejected content selected: %v", got)
	}
}

func TestRankingMigrationRejectKeepsPublished(t *testing.T) {
	a, c, _ := admissionApp(t, func(string, []rankInput) string { return `{"qualified":false,"accept":false,"reason":"poor"}` })
	old := seedAdmission(t, a, c)
	if err := a.setMeta("rankingState", ""); err != nil {
		t.Fatal(err)
	}
	m := Meme{ID: "new", Channel: "memes", Text: "poor"}
	if err := a.storeMeme(m); err != nil {
		t.Fatal(err)
	}
	if err := a.rankIncremental(c, []Meme{m}); err != nil {
		t.Fatal(err)
	}
	got, _ := a.publishedCandidates(c)
	if len(got) != len(old) {
		t.Fatalf("migration discarded published feed: %d", len(got))
	}
}

func TestRankingManualReevaluationBeforeState(t *testing.T) {
	a, c, calls := admissionApp(t, func(string, []rankInput) string { return `{"qualified":false,"accept":false,"reason":"poor"}` })
	seedAdmission(t, a, c)
	if err := a.setMeta("rankingState", ""); err != nil {
		t.Fatal(err)
	}
	if err := a.setMeta("rankingGeneration", "manual"); err != nil {
		t.Fatal(err)
	}
	if err := a.rankIncremental(c, nil); err != nil {
		t.Fatal(err)
	}
	got, _ := a.publishedCandidates(c)
	if len(got) != 0 || calls.Load() != 16 {
		t.Fatalf("manual reevaluation mistaken for migration: len=%d calls=%d", len(got), calls.Load())
	}
}

func TestRankingV2MigrationPreservesFeed(t *testing.T) {
	a, c, calls := admissionApp(t, func(string, []rankInput) string { return `{"qualified":false,"accept":false,"reason":"poor"}` })
	seedAdmission(t, a, c)
	var state rankingState
	if err := json.Unmarshal([]byte(a.meta("rankingState")), &state); err != nil {
		t.Fatal(err)
	}
	legacy, _ := json.Marshal([]string{"admission-top16-v2", rankInstruction(c), legacyAdmissionInstruction + c.InterestPrompt, c.AI.BaseURL, c.AI.Model, c.AI.Mode})
	state.Policy = fmt.Sprintf("%x:", sha256.Sum256(legacy))
	raw, _ := json.Marshal(state)
	if err := a.setMeta("rankingState", string(raw)); err != nil {
		t.Fatal(err)
	}
	if err := a.rankIncremental(c, nil); err != nil {
		t.Fatal(err)
	}
	got, _ := a.publishedCandidates(c)
	if len(got) != 16 || calls.Load() != 0 {
		t.Fatalf("version migration reset feed: len=%d calls=%d", len(got), calls.Load())
	}
}

func TestRankingVacancyRetriesSeenCandidate(t *testing.T) {
	reviews := 0
	a, c, _ := admissionApp(t, func(_ string, rows []rankInput) string {
		reviews++
		return fmt.Sprintf(`{"qualified":true,"accept":%t,"reason":"quality"}`, len(rows) == 1)
	})
	old := seedAdmission(t, a, c)
	m := Meme{ID: "new", Channel: "memes", Text: "qualified reserve"}
	if err := a.storeMeme(m); err != nil {
		t.Fatal(err)
	}
	if err := a.rankIncremental(c, []Meme{m}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec("DELETE FROM feed WHERE memeId=?", old[15].ID); err != nil {
		t.Fatal(err)
	}
	if err := a.rankIncremental(c, []Meme{m}); err != nil {
		t.Fatal(err)
	}
	got, _ := a.publishedCandidates(c)
	if len(got) != 16 || reviews != 2 {
		t.Fatalf("reserve never retried: size=%d reviews=%d", len(got), reviews)
	}
}

func TestRankingFreshnessPrefersOriginal(t *testing.T) {
	reviews := 0
	a, c, _ := admissionApp(t, func(_ string, rows []rankInput) string {
		reviews++
		if reviews == 2 && rows[1].ID != "old/002" {
			t.Errorf("freshness did not target original: %v", rows)
		}
		return `{"qualified":true,"accept":true,"reason":"quality"}`
	})
	seedAdmission(t, a, c)
	pool := []Meme{{ID: "new/1", Channel: "memes", Text: "one"}, {ID: "new/2", Channel: "memes", Text: "two"}}
	for _, m := range pool {
		if err := a.storeMeme(m); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.rankIncremental(c, pool); err != nil {
		t.Fatal(err)
	}
	if reviews != 2 {
		t.Fatalf("duplicate admission: %d", reviews)
	}
}
