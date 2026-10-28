package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLegacyConcurrencyLoad(t *testing.T) {
	a := testApp(t)
	data, err := os.ReadFile(a.configPath)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	json.Unmarshal(data, &body)
	delete(body["ai"].(map[string]any), "concurrency")
	data, _ = json.Marshal(body)
	if err := os.WriteFile(a.configPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := NewApp(a.configPath, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer loaded.Close()
	if loaded.snapshot().AI.Concurrency != 8 {
		t.Fatal("legacy default")
	}
}

func TestRetryRetainsWorkerSlot(t *testing.T) {
	var mu sync.Mutex
	attempts := map[string]int{}
	a, c := rankingModel(t, nil, nil)
	base := a.client.Transport
	var first atomic.Bool
	a.client.Transport = refreshTransport(func(r *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(r.Body)
		r.Body.Close()
		r.Body = io.NopCloser(strings.NewReader(string(body)))
		key := string(body)
		mu.Lock()
		attempts[key]++
		n := attempts[key]
		unique := len(attempts)
		mu.Unlock()
		if unique > 1 && !first.Load() {
			t.Error("retry released worker slot")
		}
		if n == 1 {
			w := httptest.NewRecorder()
			w.WriteHeader(503)
			return w.Result(), nil
		}
		first.Store(true)
		return base.RoundTrip(r)
	})
	c.AI.Concurrency = 1
	if _, err := a.rankCandidates(c, rankTestCandidates(9)); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, n := range attempts {
		if n != 2 {
			t.Fatal(attempts)
		}
	}
}

func TestConcurrencyConfigAPI(t *testing.T) {
	a := testApp(t)
	session := login(t, a)
	for _, value := range []string{"", "0", "1", "32", "-1", "33", "1.5", "\"8\""} {
		c := a.snapshot()
		b, _ := json.Marshal(c)
		var body map[string]any
		json.Unmarshal(b, &body)
		ai := body["ai"].(map[string]any)
		delete(ai, "concurrency")
		if value != "" {
			ai["concurrency"] = json.RawMessage(value)
		}
		b, _ = json.Marshal(body)
		w := req(a, "PUT", "/api/admin/config", string(b), session)
		valid := value == "" || value == "0" || value == "1" || value == "32"
		if !valid {
			checkCode(t, w, 400)
			continue
		}
		checkCode(t, w, 200)
		want := 8
		if value == "1" {
			want = 1
		}
		if value == "32" {
			want = 32
		}
		w = req(a, "GET", "/api/admin/config", "", session)
		var got Config
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.AI.Concurrency != want {
			t.Fatal(got.AI)
		}
		data, err := os.ReadFile(a.configPath)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatal(err)
		}
		if got.AI.Concurrency != want {
			t.Fatal("not persisted", got.AI)
		}
	}
	c := a.snapshot()
	c.AI.Concurrency = 0
	if err := a.save(c); err != nil || a.snapshot().AI.Concurrency != 8 {
		t.Fatal(err)
	}
	c.AI.Concurrency = -1
	if err := a.save(c); err == nil {
		t.Fatal("invalid direct save")
	}
}

func TestRankingConcurrencyAndOrder(t *testing.T) {
	for _, limit := range []int{0, 1, 3, 32} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			var active, peak, mergeActive, mergePeak atomic.Int32
			update := func(p *atomic.Int32, n int32) {
				for {
					old := p.Load()
					if n <= old || p.CompareAndSwap(old, n) {
						return
					}
				}
			}
			a, c := rankingModel(t, nil, func(_ string, rows []rankInput, _ []string) {
				update(&peak, active.Add(1))
				defer active.Add(-1)
				if len(rows) == 16 {
					update(&mergePeak, mergeActive.Add(1))
					defer mergeActive.Add(-1)
				}
				time.Sleep(time.Duration(2+int(rows[0].ID[2])%5) * time.Millisecond)
			})
			c.AI.Concurrency = limit
			got, err := a.rankCandidates(c, rankTestCandidates(100))
			if err != nil {
				t.Fatal(err)
			}
			effective := limit
			if effective == 0 {
				effective = 8
			}
			if peak.Load() > int32(effective) || peak.Load() < int32(min(effective, 2)) {
				t.Fatalf("peak=%d limit=%d", peak.Load(), effective)
			}
			if effective > 1 && mergePeak.Load() < 2 {
				t.Fatal("merges not concurrent")
			}
			for i, m := range got {
				if m.ID != fmt.Sprintf("%03d", 99-i) {
					t.Fatal(got)
				}
			}
			if c.AI.Concurrency != limit {
				t.Fatal("snapshot mutated")
			}
		})
	}
}

func TestParallelIndexedCompletion(t *testing.T) {
	release := make(chan struct{})
	got, err := rankParallel(context.Background(), 2, 2, func(ctx context.Context, i int) ([]Meme, error) {
		if i == 0 {
			<-release
		} else {
			close(release)
		}
		return []Meme{{ID: fmt.Sprint(i)}}, nil
	})
	if err != nil || !reflect.DeepEqual(got, [][]Meme{{{ID: "0"}}, {{ID: "1"}}}) {
		t.Fatalf("%v %v", got, err)
	}
}

func TestIncrementalCancellationPreservesFeed(t *testing.T) {
	a := testApp(t)
	c := a.snapshot()
	c.AI.Model = "test"
	c.AI.Concurrency = 2
	a.config = c
	items := rankTestCandidates(24)
	for i := range items {
		items[i].ID = fmt.Sprintf("memes/%d", i+1)
		items[i].Channel = "memes"
		if err := a.storeMeme(items[i]); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.publishFeed(c, items[:1], "previous"); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	var calls, canceled atomic.Int32
	a.client = &http.Client{Transport: refreshTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "t.me" {
			w := httptest.NewRecorder()
			fmt.Fprint(w, adapterPostHTML(items[0].ID, items[0].Text, "", "2026-01-01T00:00:00Z"))
			return w.Result(), nil
		}
		io.Copy(io.Discard, r.Body)
		r.Body.Close()
		if calls.Add(1) == 1 {
			close(started)
			<-r.Context().Done()
			canceled.Add(1)
			return nil, r.Context().Err()
		}
		<-release
		w := httptest.NewRecorder()
		w.WriteHeader(400)
		return w.Result(), nil
	})}
	if !a.launch(false) {
		t.Fatal("launch")
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		close(release)
		a.cancel()
		t.Fatal("ranking did not start")
	}
	if a.launch(true) || a.launch(false) {
		t.Error("tasks overlapped")
	}
	close(release)
	a.cancel()
	done := make(chan struct{})
	go func() { a.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		a.cancel()
		t.Fatal("workers leaked")
	}
	if canceled.Load() != 1 || calls.Load() != 1 {
		t.Fatalf("calls=%d canceled=%d", calls.Load(), canceled.Load())
	}
	var id string
	var count int
	if err := a.db.QueryRow("SELECT count(*),min(memeId) FROM feed").Scan(&count, &id); err != nil {
		t.Fatal(err)
	}
	if count != 1 || id != items[0].ID || a.meta("rankingIdentity") != "previous" || !strings.Contains(a.status.LastError, "批量排序失败") {
		t.Fatalf("partial publication: %s %s", id, a.status.LastError)
	}
}
