package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

type feedbackCancelBody struct {
	io.Reader
	cancel context.CancelFunc
}

func (b *feedbackCancelBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	if err == io.EOF {
		b.cancel()
	}
	return n, err
}

func (b *feedbackCancelBody) Close() error { return nil }

func TestOptimizeCanceledBeforeCommit(t *testing.T) {
	const nextPrompt = "新的兴趣"
	for _, directCall := range []bool{true, false} {
		name := "optimize_preserves_state"
		if directCall {
			name = "CallAI_succeeds_despite_EOF_cancellation"
		}
		t.Run(name, func(t *testing.T) {
			a := testApp(t)
			c := a.snapshot()
			c.AI.BaseURL = "http://localhost"
			c.AI.Model = "test"
			c.AI.Mode = "chat"
			commitCtx, cancelCommit := context.WithCancel(a.ctx)
			defer cancelCommit()
			calls := 0
			a.client = &http.Client{Transport: aiRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if err := r.Context().Err(); err != nil {
					t.Fatalf("request canceled before response: %v", err)
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": {"application/json"}},
					Body: &feedbackCancelBody{
						Reader: strings.NewReader(`{"choices":[{"message":{"content":"` + nextPrompt + `"}}]}`),
						cancel: func() {
							// Keep the captured request context live so fetchAI returns success.
							cancelCommit()
							a.ctx = commitCtx
						},
					},
				}, nil
			})}
			if directCall {
				prompt, err := CallAI(a.aiContext(a.ctx), a.client, c.AI, "总结兴趣", "样本", nil)
				if err != nil || prompt != nextPrompt {
					t.Fatalf("CallAI = %q, %v; want successful result %q", prompt, err, nextPrompt)
				}
			} else {
				if err := a.storeMeme(Meme{ID: "memes/42", Text: "原帖文字", Channel: "memes"}); err != nil {
					t.Fatal(err)
				}
				if _, err := a.db.Exec("INSERT INTO copies(id,memeId,kind,visitor,createdAt,imageIndex) VALUES(?,?,?,?,?,?)", 8, "memes/42", "text", "visitor", "now", -1); err != nil {
					t.Fatal(err)
				}
				beforeMeta := map[string]string{"lastOptimization": "2025-01-01T00:00:00Z", "optimizedCopyId": "7"}
				for key, value := range beforeMeta {
					if err := a.setMeta(key, value); err != nil {
						t.Fatal(err)
					}
				}
				a.status.LastOptimization = beforeMeta["lastOptimization"]
				beforeConfig, err := os.ReadFile(a.configPath)
				if err != nil {
					t.Fatal(err)
				}
				if err := a.optimize(c); !errors.Is(err, context.Canceled) {
					t.Errorf("optimize error = %v; want context.Canceled", err)
				}
				if got := a.snapshot().InterestPrompt; got != c.InterestPrompt {
					t.Errorf("InterestPrompt changed to %q", got)
				}
				afterConfig, err := os.ReadFile(a.configPath)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(beforeConfig, afterConfig) {
					t.Error("persisted configuration changed")
				}
				for key, want := range beforeMeta {
					if got := a.meta(key); got != want {
						t.Errorf("%s = %q; want %q", key, got, want)
					}
				}
				if a.status.LastOptimization != beforeMeta["lastOptimization"] {
					t.Error("status.LastOptimization changed")
				}
			}
			if calls != 1 || !errors.Is(a.ctx.Err(), context.Canceled) {
				t.Fatalf("calls = %d, context error = %v; want one successful response followed by cancellation", calls, a.ctx.Err())
			}
		})
	}
}
