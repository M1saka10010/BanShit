package app

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
)

func TestIncrementalAllCandidatesAndPolicy(t *testing.T) {
	var calls atomic.Int32
	model, mc := rankingModel(t, nil, func(string, []rankInput, []string) { calls.Add(1) })
	a := testApp(t)
	c := a.snapshot()
	c.AI = mc.AI
	c.Channels = []string{""}
	a.config = c
	a.client = model.client
	pool := rankTestCandidates(20)
	for _, m := range pool {
		if err := a.storeMeme(m); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.rankIncremental(c, append(pool, pool[0])); err != nil {
		t.Fatal(err)
	}
	var initial rankingState
	if err := json.Unmarshal([]byte(a.meta("rankingState")), &initial); err != nil {
		t.Fatal(err)
	}
	if len(initial.Seen) != 20 || len(initial.Top) != rankTop {
		t.Fatalf("incomplete single round: %+v", initial)
	}
	published := a.meta("lastRun")
	a = &App{ctx: a.ctx, db: a.db, config: c, client: model.client}
	before := calls.Load()
	if err := a.rankIncremental(c, pool); err != nil {
		t.Fatal(err)
	}
	if a.meta("lastRun") != published {
		t.Fatal("no-op republished feed")
	}
	c.AI.APIKey = "rotated"
	c.AI.Concurrency = 2
	a.config = c
	if err := a.rankIncremental(c, pool); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != before {
		t.Fatal("unchanged policy called AI")
	}
	if err := a.rankIncremental(c, pool[:19]); err != nil {
		t.Fatal(err)
	}
	var state rankingState
	json.Unmarshal([]byte(a.meta("rankingState")), &state)
	if fmt.Sprint(state.Top) != fmt.Sprint(initial.Top) {
		t.Fatal("published top expired with candidate window")
	}
	c.InterestPrompt = "changed"
	a.config = c
	if err := a.rankIncremental(c, pool); err != nil {
		t.Fatal(err)
	}
	if calls.Load() == before {
		t.Fatal("policy not reevaluated")
	}
}

func TestRankingFingerprintMediaContent(t *testing.T) {
	b := []byte("same image bytes")
	inline := Meme{Text: "text", Images: []string{"data:image/png;base64," + base64.StdEncoding.EncodeToString(b)}}
	ref := Meme{Text: "text", Images: []string{fmt.Sprintf("media:%x.png", sha256.Sum256(b))}}
	if rankFingerprint(inline) != rankFingerprint(ref) {
		t.Fatal("storage migration changed fingerprint")
	}
}

func TestIncrementalFailureNotMarked(t *testing.T) {
	model, mc := rankingModel(t, func([]rankInput) string { return "{}" }, nil)
	a := testApp(t)
	c := a.snapshot()
	c.AI = mc.AI
	a.config = c
	a.client = model.client
	pool := rankTestCandidates(4)
	for _, m := range pool {
		if err := a.storeMeme(m); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.publishFeed(c, pool[:1]); err != nil {
		t.Fatal(err)
	}
	if err := a.rankIncremental(c, pool); err == nil {
		t.Fatal("expected failure")
	}
	if a.meta("rankingState") != "" {
		t.Fatal("failure persisted work")
	}
	var id string
	a.db.QueryRow("SELECT memeId FROM feed").Scan(&id)
	if id != pool[0].ID {
		t.Fatal("failure published")
	}
}
