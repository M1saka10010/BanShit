package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestOptimizeUsesCopiedMedia(t *testing.T) {
	for _, kind := range []string{"image", "text", "album"} {
		t.Run(kind, func(t *testing.T) {
			a := testApp(t)
			pics := []string{previewFixture(t, 80, 40, 1, false), previewFixture(t, 80, 40, 2, false), previewFixture(t, 80, 40, 3, false)}
			previews := make([]string, len(pics))
			for i, pic := range pics {
				var err error
				previews[i], err = aiImagePreview(pic)
				if err != nil || previews[i] == pic {
					t.Fatalf("fixture must compress: %v", err)
				}
			}
			if e := a.storeMeme(Meme{ID: "memes/42", Text: "原帖文字", Images: pics, Channel: "memes"}); e != nil {
				t.Fatal(e)
			}
			index := -1
			if kind == "image" {
				index = 1
			}
			if _, e := a.db.Exec("INSERT INTO copies(memeId,kind,visitor,createdAt,imageIndex) VALUES(?,?,?,?,?)", "memes/42", kind, "visitor", "now", index); e != nil {
				t.Fatal(e)
			}
			captured := make(chan []byte, 1)
			// Repeated operations remain in history but count as one visitor.
			if _, e := a.db.Exec("INSERT INTO copies(memeId,kind,visitor,createdAt,imageIndex) VALUES(?,?,?,?,?)", "memes/42", kind, "visitor", "now", index); e != nil {
				t.Fatal(e)
			}
			mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Messages []struct{ Content json.RawMessage }
				}
				if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
					t.Error(e)
					return
				}
				captured <- body.Messages[1].Content
				json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": "新的兴趣"}}}})
			}))
			defer mock.Close()
			c := a.snapshot()
			c.AI.BaseURL = mock.URL
			c.AI.Model = "test"
			if e := a.optimize(c); e != nil {
				t.Fatal(e)
			}
			var stored string
			if err := a.db.QueryRow("SELECT images FROM memes WHERE id=?", "memes/42").Scan(&stored); err != nil {
				t.Fatal(err)
			}
			originals, err := decodeImages(stored)
			if err != nil || !reflect.DeepEqual(originals, pics) {
				t.Fatal("stored originals changed", err)
			}
			var content []struct {
				Type     string
				Text     string
				ImageURL struct {
					URL string `json:"url"`
				} `json:"image_url"`
			}
			if e := json.Unmarshal(<-captured, &content); e != nil {
				t.Fatal(e)
			}
			want := 0
			if kind == "image" {
				want = 1
			}
			if kind == "album" {
				want = 3
			}
			if len(content) != want+1 {
				t.Fatalf("got %d images want %d", len(content)-1, want)
			}
			var payload struct {
				Samples []struct {
					Text         string
					CopyKind     string
					Votes        int
					ImageIndexes []int
				}
			}
			if e := json.Unmarshal([]byte(content[0].Text), &payload); e != nil {
				t.Fatal(e)
			}
			if len(payload.Samples) != 1 || payload.Samples[0].Text != "原帖文字" || payload.Samples[0].CopyKind != kind || len(payload.Samples[0].ImageIndexes) != want {
				t.Fatalf("bad sample: %+v", payload)
			}
			if kind == "image" && content[1].ImageURL.URL != previews[1] {
				t.Fatal("wrong image selected")
			}
			if payload.Samples[0].Votes != 1 {
				t.Fatal("repeat copy inflated votes")
			}
			if kind == "album" {
				for i := range pics {
					if content[i+1].ImageURL.URL != previews[i] {
						t.Fatal("album order changed")
					}
				}
			}
		})
	}
}
