package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
)

func admissionApp(t *testing.T, decide func(string, []rankInput) string) (*App, Config, *atomic.Int32) {
	t.Helper()
	calls := new(atomic.Int32)
	model, mc := rankingModel(t, nil, func(string, []rankInput, []string) { calls.Add(1) }, decide)
	a := testApp(t)
	c := a.snapshot()
	c.AI = mc.AI
	c.AI.Concurrency = 1
	c.Channels = []string{"memes"}
	a.config, a.client = c, model.client
	return a, c, calls
}

func seedAdmission(t *testing.T, a *App, c Config) []Meme {
	t.Helper()
	old := rankTestCandidates(16)
	state := rankingState{Policy: rankingPolicy(c) + ":" + a.meta("rankingGeneration"), Seen: map[string]bool{}}
	for i := range old {
		old[i].ID = fmt.Sprintf("old/%03d", 16-i)
		old[i].Channel = "memes"
		old[i].CreatedAt = "2020-01-01T00:00:00Z"
		if err := a.storeMeme(old[i]); err != nil {
			t.Fatal(err)
		}
		state.Top = append(state.Top, old[i].ID)
		state.Seen[fingerprintKey(old[i])] = true
	}
	b, _ := json.Marshal(state)
	if err := a.publishFeed(c, old, "seed", string(b)); err != nil {
		t.Fatal(err)
	}
	if err := a.setMeta("lastRun", "seed time"); err != nil {
		t.Fatal(err)
	}
	a.status.LastRun = "seed time"
	return old
}

func TestAdmissionSingleAndInvalid(t *testing.T) {
	for _, output := range []string{`{"qualified":false,"accept":false,"reason":"poor"}`, `{}`, `{"qualified":true,"accept":true}`, `{"accept":"yes","reason":"x"}`, `{"qualified":true,"accept":true,"reason":""}`, `{"qualified":true,"accept":true,"reason":"x","extra":1}`, `{"qualified":true,"accept":true,"reason":"x"} {}`} {
		t.Run(output, func(t *testing.T) {
			a, c, calls := admissionApp(t, func(_ string, rows []rankInput) string {
				if len(rows) != 1 {
					t.Error("single candidate expected")
				}
				return output
			})
			m := Meme{ID: "single", Channel: "memes", Text: "poor"}
			if err := a.storeMeme(m); err != nil {
				t.Fatal(err)
			}
			err := a.rankIncremental(c, []Meme{m})
			valid := strings.Contains(output, `"accept":false`)
			if (err == nil) != valid {
				t.Fatalf("error=%v", err)
			}
			if calls.Load() != 1 {
				t.Fatal("single candidate bypassed AI")
			}
			got, _ := a.publishedCandidates(c)
			if len(got) != 0 {
				t.Fatal("rejected candidate published")
			}
			if !valid && a.meta("rankingState") != "" {
				t.Fatal("failure saved state")
			}
		})
	}
}

func TestAdmissionChannelAndPolicyChanges(t *testing.T) {
	for _, change := range []string{"channel", "policy", "manual"} {
		t.Run(change, func(t *testing.T) {
			a, c, calls := admissionApp(t, func(string, []rankInput) string { return `{"qualified":false,"accept":false,"reason":"unsuitable"}` })
			seedAdmission(t, a, c)
			if err := a.rankIncremental(c, nil); err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 0 {
				t.Fatal("window expiration called AI")
			}
			switch change {
			case "channel":
				c.Channels = []string{"other"}
			case "policy":
				c.InterestPrompt = "changed"
			case "manual":
				if err := a.setMeta("rankingGeneration", "next"); err != nil {
					t.Fatal(err)
				}
			}
			a.config = c
			if err := a.rankIncremental(c, nil); err != nil {
				t.Fatal(err)
			}
			got, err := a.publishedCandidates(c)
			if err != nil || len(got) != 0 {
				t.Fatalf("old content exempted %v %v", got, err)
			}
			expected := int32(16)
			if change == "channel" {
				expected = 0
			}
			if calls.Load() != expected {
				t.Fatalf("calls=%d", calls.Load())
			}
		})
	}
}

func TestAdmissionStrongCandidateReplacesFresh(t *testing.T) {
	a, c, _ := admissionApp(t, func(instruction string, rows []rankInput) string {
		accept := rows[0].ID == "new/1" || rows[1].ID == "new/1"
		if rows[1].ID == "new/1" && !strings.Contains(instruction, "来自原首页：false") {
			t.Error("fresh target received freshness preference")
		}
		return fmt.Sprintf(`{"qualified":true,"accept":%t,"reason":"comparison"}`, accept)
	})
	seedAdmission(t, a, c)
	pool := []Meme{{ID: "new/1", Channel: "memes", Text: "fresh one"}, {ID: "new/2", Channel: "memes", Text: "fresh two"}}
	for _, m := range pool {
		if err := a.storeMeme(m); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.rankIncremental(c, pool); err != nil {
		t.Fatal(err)
	}
	got, _ := a.publishedCandidates(c)
	if len(got) != 16 || got[15].ID != "new/2" {
		t.Fatalf("strong fresh not selected: %v", got)
	}
}

func TestAdmissionFailureRetry(t *testing.T) {
	var reviews atomic.Int32
	a, c, _ := admissionApp(t, func(string, []rankInput) string {
		if reviews.Add(1) == 2 {
			return `{}`
		}
		return `{"qualified":true,"accept":true,"reason":"good"}`
	})
	old := seedAdmission(t, a, c)
	before := a.meta("rankingState")
	pool := []Meme{{ID: "new/1", Channel: "memes", Text: "fresh one"}, {ID: "new/2", Channel: "memes", Text: "fresh two"}}
	for _, m := range pool {
		if err := a.storeMeme(m); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.rankIncremental(c, pool); err == nil {
		t.Fatal("expected failure")
	}
	got, _ := a.publishedCandidates(c)
	for i := range old {
		if got[i].ID != old[i].ID {
			t.Fatal("partial publication")
		}
	}
	if a.meta("rankingState") != before || a.meta("lastRun") != "seed time" {
		t.Fatal("failure saved progress")
	}
	if err := a.rankIncremental(c, pool); err != nil {
		t.Fatal(err)
	}
	if reviews.Load() != 4 {
		t.Fatal("whole round not retried")
	}
}

func TestAdmissionRotationCounts(t *testing.T) {
	for _, count := range []int{0, 2, 4, 7, 16, 17} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			accepted := 0
			a, c, calls := admissionApp(t, func(instruction string, rows []rankInput) string {
				if !strings.Contains(instruction, fmt.Sprintf("本轮当前实际换入数：%d；", min(accepted, 16))) {
					t.Errorf("wrong live replacement count: %s", instruction)
				}
				if len(rows) != 2 {
					t.Errorf("missing replacement: %v", rows)
				}
				accept := accepted < count
				if accept {
					if accepted < 16 && !strings.HasPrefix(rows[1].ID, "old/") {
						t.Errorf("replacing fresh selection prematurely: %v", rows)
					}
					accepted++
				}
				return fmt.Sprintf(`{"qualified":true,"accept":%t,"reason":"quality decision"}`, accept)
			})
			old := seedAdmission(t, a, c)
			pool := rankTestCandidates(100)
			for i := range pool {
				pool[i].ID = fmt.Sprintf("new/%03d", i)
				pool[i].Text = "fresh " + pool[i].ID
				pool[i].Channel = "memes"
				pool[i].CreatedAt = "2026-01-01T00:00:00Z"
				if err := a.storeMeme(pool[i]); err != nil {
					t.Fatal(err)
				}
			}
			window, err := a.rankingPool(c)
			if err != nil || len(window) != 100 {
				t.Fatalf("window: %d %v", len(window), err)
			}
			if err := a.rankIncremental(c, append(window, window[0])); err != nil {
				t.Fatal(err)
			}
			got, err := a.publishedCandidates(c)
			if err != nil {
				t.Fatal(err)
			}
			fresh := 0
			for _, m := range got {
				if strings.HasPrefix(m.ID, "new/") {
					fresh++
				}
			}
			if fresh != min(count, 16) || len(got) != 16 {
				t.Fatalf("fresh=%d size=%d", fresh, len(got))
			}
			if count == 0 {
				for i := range old {
					if old[i].ID != got[i].ID {
						t.Fatal("all rejected changed order")
					}
				}
				if a.meta("lastRun") != "seed time" || a.status.LastRun != "seed time" {
					t.Fatal("rejection changed publication time")
				}
			}
			before := calls.Load()
			if err := a.rankIncremental(c, window); err != nil {
				t.Fatal(err)
			}
			if calls.Load() != before {
				t.Fatal("unchanged window called AI")
			}
		})
	}
}
