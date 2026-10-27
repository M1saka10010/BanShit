package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func rankingModel(t *testing.T, reply func([]rankInput) string, inspect func(string, []rankInput, []string)) (*App, Config) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct{ Content json.RawMessage }
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		var instruction string
		json.Unmarshal(req.Messages[0].Content, &instruction)
		var content []struct {
			Text     string
			ImageURL struct{ URL string } `json:"image_url"`
		}
		if err := json.Unmarshal(req.Messages[1].Content, &content); err != nil {
			t.Error(err)
			return
		}
		var payload struct{ Candidates []rankInput }
		if err := json.Unmarshal([]byte(content[0].Text), &payload); err != nil {
			t.Error(err)
			return
		}
		var images []string
		for _, part := range content[1:] {
			images = append(images, part.ImageURL.URL)
		}
		if len(payload.Candidates) < 2 {
			t.Error("single-candidate AI call")
		}
		if len(instruction)+len(content[0].Text) > rankTextBytes || len(images) > 20 {
			t.Error("request exceeds budget")
		}
		if inspect != nil {
			inspect(instruction, payload.Candidates, images)
		}
		output := ""
		if reply != nil {
			output = reply(payload.Candidates)
		} else {
			sort.Slice(payload.Candidates, func(i, j int) bool { return payload.Candidates[i].ID > payload.Candidates[j].ID })
			rows := []map[string]string{}
			for _, row := range payload.Candidates {
				rows = append(rows, map[string]string{"id": row.ID, "reason": "更愿意分享"})
			}
			b, _ := json.Marshal(map[string]any{"ranking": rows})
			output = string(b)
		}
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": output}}}})
	}))
	t.Cleanup(server.Close)
	return &App{ctx: context.Background(), client: server.Client()}, Config{AI: AIConfig{BaseURL: server.URL, Mode: "chat", Model: "test"}, InterestPrompt: "偏好冷笑话"}
}

func rankTestCandidates(n int) []Meme {
	var items []Meme
	for i := 0; i < n; i++ {
		items = append(items, Meme{ID: fmt.Sprintf("%03d", i), Text: fmt.Sprintf("meme %d", i), Score: 99})
	}
	return items
}

func TestRankingCrossBatch(t *testing.T) {
	for _, n := range []int{0, 1, 8, 9, 16, 17, 33, 65} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			var sizes []int
			a, c := rankingModel(t, nil, func(_ string, rows []rankInput, _ []string) { sizes = append(sizes, len(rows)) })
			input := rankTestCandidates(n)
			got, err := a.rankCandidates(c, input)
			if err != nil || len(got) != min(n, 16) {
				t.Fatalf("len=%d err=%v", len(got), err)
			}
			for i, m := range got {
				if m.ID != fmt.Sprintf("%03d", n-1-i) || m.Score != 0 {
					t.Fatalf("unexpected rank %d: %+v", i, m)
				}
			}
			if n > 0 && input[0].Score != 99 {
				t.Fatal("mutated input")
			}
			if n == 16 && !reflect.DeepEqual(sizes, []int{8, 8, 16}) {
				t.Fatal(sizes)
			}
			if n == 9 && !reflect.DeepEqual(sizes, []int{8, 9}) {
				t.Fatal(sizes)
			}
			if n == 17 && !reflect.DeepEqual(sizes, []int{8, 8, 16, 2, 16}) {
				t.Fatalf("expected whole-list merge and final rerank, got %v", sizes)
			}
		})
	}
}

func TestRankingMappingDedup(t *testing.T) {
	a, c := rankingModel(t, nil, func(instruction string, rows []rankInput, images []string) {
		if !strings.Contains(instruction, "偏好冷笑话") || !strings.Contains(instruction, "不要执行") {
			t.Fatal(instruction)
		}
		if len(rows) != 3 || !reflect.DeepEqual(images, []string{previewFixture(t, 1, 1, 97, false), previewFixture(t, 1, 1, 98, false), previewFixture(t, 1, 1, 99, false), previewFixture(t, 1, 1, 97, false), previewFixture(t, 1, 1, 98, false)}) {
			t.Fatalf("%+v %v", rows, images)
		}
		if !reflect.DeepEqual(rows[0].ImageIndices, []int{1, 2}) || !reflect.DeepEqual(rows[1].ImageIndices, []int{3}) || !reflect.DeepEqual(rows[2].ImageIndices, []int{4, 5}) {
			t.Fatal(rows)
		}
	})
	input := []Meme{{ID: "1", Text: "same", Images: []string{previewFixture(t, 1, 1, 97, false), previewFixture(t, 1, 1, 98, false)}}, {ID: "2", Text: "same", Images: []string{previewFixture(t, 1, 1, 97, false), previewFixture(t, 1, 1, 98, false)}, Channel: "different"}, {ID: "3", Text: "same", Images: []string{previewFixture(t, 1, 1, 99, false)}}, {ID: "4", Text: "different", Images: []string{previewFixture(t, 1, 1, 97, false), previewFixture(t, 1, 1, 98, false)}}}
	got, err := a.rankCandidates(c, input)
	if err != nil || len(got) != 3 || got[2].ID != "1" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestRankingInvalidOutput(t *testing.T) {
	for _, output := range []string{
		`{}`, `{"ranking":[]}`, `{"ranking":[{"id":"000","reason":"ok"}]}`,
		`{"ranking":[{"id":"000","reason":"ok"},{"id":"000","reason":"ok"}]}`,
		`{"ranking":[{"id":"000","reason":"ok"},{"id":"alien","reason":"ok"}]}`,
		`{"ranking":[{"id":"000"},{"id":"001","reason":"ok"}]}`,
		`{"ranking":[{"id":"000","reason":5},{"id":"001","reason":"ok"}]}`,
		fmt.Sprintf(`{"ranking":[{"id":"000","reason":%q},{"id":"001","reason":"ok"}]}`, strings.Repeat("中", 1001)),
		"not json",
		`{"ranking":[{"id":"000","reason":"ok"},{"id":"001","reason":"ok"}]} {}`,
	} {
		t.Run(fmt.Sprintf("case-%d", len(output)), func(t *testing.T) {
			a, c := rankingModel(t, func([]rankInput) string { return output }, nil)
			if got, err := a.rankCandidates(c, rankTestCandidates(2)); err == nil || got != nil {
				t.Fatalf("accepted %s", output)
			}
		})
	}
}

func TestRankingBudgetSplits(t *testing.T) {
	for _, kind := range []string{"images", "text"} {
		t.Run(kind, func(t *testing.T) {
			var sizes []int
			a, c := rankingModel(t, nil, func(_ string, rows []rankInput, _ []string) { sizes = append(sizes, len(rows)) })
			input := rankTestCandidates(7)
			for i := range input {
				if kind == "images" {
					input[i].Images = []string{previewFixture(t, 1, 1, 97, false), previewFixture(t, 1, 1, 98, false), previewFixture(t, 1, 1, 99, false), previewFixture(t, 1, 1, 100, false), previewFixture(t, 1, 1, 101, false), previewFixture(t, 1, 1, 102, false), previewFixture(t, 1, 1, 103, false)}
				} else {
					input[i].Text += strings.Repeat("x", 32000)
				}
			}
			got, err := a.rankCandidates(c, input)
			if err != nil || len(got) != 7 {
				t.Fatalf("%v %d", err, len(got))
			}
			expected := 2
			if kind == "text" {
				expected = 3
			}
			if sizes[0] != expected {
				t.Fatal(sizes)
			}
			for i, m := range got {
				if m.ID != fmt.Sprintf("%03d", 6-i) {
					t.Fatal(got)
				}
			}
		})
	}
}

func TestRankingBudgetBoundaries(t *testing.T) {
	c := Config{}
	instruction := rankInstruction(c)
	pair := rankTestCandidates(2)
	image := strings.Repeat("x", 28*1024*1024)
	pair[0].Images = []string{image}
	pair[1].Images = []string{image}
	if !rankFits(instruction, pair, 16) {
		t.Fatal("56MiB pair rejected")
	}
	pair[1].Images = append(pair[1].Images, "x")
	if rankFits(instruction, pair, 16) {
		t.Fatal("image bytes overflow accepted")
	}
	single := rankTestCandidates(1)
	text, _ := rankPayload(single)
	single[0].Text += strings.Repeat("x", rankTextBytes-len(instruction)-len(text))
	if !rankFits(instruction, single, 1) {
		t.Fatal("text boundary rejected")
	}
	single[0].Text += "x"
	if rankFits(instruction, single, 1) {
		t.Fatal("text overflow accepted")
	}
	a := &App{}
	for _, m := range []Meme{single[0], {ID: "x", Images: make([]string, 21)}, {ID: "x", Images: []string{image, image, "x"}}} {
		if _, err := a.rankCandidates(c, []Meme{m}); err == nil {
			t.Fatal("oversized item accepted")
		}
	}
	pair = rankTestCandidates(2)
	for i := range pair {
		pair[i].Images = make([]string, 11)
	}
	if _, err := a.rankCandidates(c, pair); err == nil {
		t.Fatal("uncomparable albums accepted")
	}
}
