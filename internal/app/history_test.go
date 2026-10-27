package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestCopyHistory(t *testing.T) {
	a := testApp(t)
	checkCode(t, req(a, "GET", "/api/admin/copies", ""), 401)
	session := login(t, a)
	w := req(a, "GET", "/api/admin/copies", "", session)
	checkCode(t, w, 200)
	if !strings.Contains(w.Body.String(), "\"items\":[]") {
		t.Fatal(w.Body.String())
	}
	pics := []string{"data:image/png;base64,YQ==", "data:image/png;base64,Yg=="}
	if e := a.storeMeme(Meme{ID: "memes/1", Text: "original text", Images: pics, Channel: "memes"}); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 7; i++ {
		kind := "image"
		index := 1
		if i == 5 {
			kind = "text"
			index = -1
		}
		if i == 6 {
			kind = "album"
			index = -1
		}
		if _, e := a.db.Exec("INSERT INTO copies(memeId,kind,imageIndex,visitor,createdAt) VALUES(?,?,?,?,?)", "memes/1", kind, index, fmt.Sprint("private-cookie-", i), "2026-01-01T00:00:00Z"); e != nil {
			t.Fatal(e)
		}
	}
	w = req(a, "GET", "/api/admin/copies", "", session)
	checkCode(t, w, 200)
	var page struct {
		Items []struct {
			ID                  int
			Kind, Text, Visitor string
			Images              []string
		}
		NextBefore int
	}
	if e := json.Unmarshal(w.Body.Bytes(), &page); e != nil {
		t.Fatal(e)
	}
	if len(page.Items) != 5 || page.Items[0].ID != 7 || page.NextBefore != 3 {
		t.Fatal(page)
	}
	if len(page.Items[0].Images) != 2 || len(page.Items[1].Images) != 0 || len(page.Items[2].Images) != 1 || page.Items[2].Images[0] != pics[1] {
		t.Fatal("incorrect copied media")
	}
	if strings.Contains(w.Body.String(), "private-cookie") {
		t.Fatal("raw visitor exposed")
	}
	w = req(a, "GET", "/api/admin/copies?before=3", "", session)
	checkCode(t, w, 200)
	if e := json.Unmarshal(w.Body.Bytes(), &page); e != nil {
		t.Fatal(e)
	}
	if len(page.Items) != 2 || page.Items[0].ID != 2 || page.NextBefore != 0 {
		t.Fatal(page)
	}
	checkCode(t, req(a, "GET", "/api/admin/copies?before=bad", "", session), 400)
	for _, query := range []string{"page=0", "page=abc", "start=bad", "start=2026-02-01T00:00:00Z&end=2026-01-01T00:00:00Z"} {
		checkCode(t, req(a, "GET", "/api/admin/copies?"+query, "", session), 400)
	}
	for _, tc := range []struct {
		query                     string
		total, pages, page, count int
	}{
		{"page=2", 7, 2, 2, 2}, {"page=999", 7, 2, 2, 2},
		{"start=2026-01-01T08:00:00%2B08:00&end=2026-01-01T00:00:00Z", 7, 2, 1, 5},
		{"start=2026-01-01T00:00:01Z", 0, 0, 1, 0}, {"end=2025-12-31T23:59:59Z", 0, 0, 1, 0},
	} {
		w = req(a, "GET", "/api/admin/copies?"+tc.query, "", session)
		checkCode(t, w, 200)
		var result struct {
			Total, TotalPages, Page int
			Items                   []any
		}
		if e := json.Unmarshal(w.Body.Bytes(), &result); e != nil {
			t.Fatal(e)
		}
		if result.Total != tc.total || result.TotalPages != tc.pages || result.Page != tc.page || len(result.Items) != tc.count {
			t.Fatalf("%s: %+v", tc.query, result)
		}
	}
}

func TestCopyHistorySnapshot(t *testing.T) {
	a := testApp(t)
	session := login(t, a)
	if e := a.storeMeme(Meme{ID: "memes/1", Text: "text", Channel: "memes"}); e != nil {
		t.Fatal(e)
	}
	add := func() {
		t.Helper()
		if _, e := a.db.Exec("INSERT INTO copies(memeId,kind,imageIndex,visitor,createdAt) VALUES('memes/1','text',-1,'visitor','2026-01-01T00:00:00Z')"); e != nil {
			t.Fatal(e)
		}
	}
	for i := 0; i < 10; i++ {
		add()
	}
	var result struct {
		Snapshot, Total int
		Items           []struct{ ID int }
	}
	read := func(query string) {
		t.Helper()
		w := req(a, "GET", "/api/admin/copies"+query, "", session)
		checkCode(t, w, 200)
		if e := json.Unmarshal(w.Body.Bytes(), &result); e != nil {
			t.Fatal(e)
		}
	}
	read("")
	if result.Snapshot != 10 || result.Items[0].ID != 10 {
		t.Fatal(result)
	}
	add()
	read("?page=2&snapshot=10")
	if result.Total != 10 || len(result.Items) != 5 || result.Items[0].ID != 5 {
		t.Fatal(result)
	}
	read("")
	if result.Total != 11 || result.Snapshot != 11 || result.Items[0].ID != 11 {
		t.Fatal(result)
	}
	read("?snapshot=0")
	if result.Total != 0 {
		t.Fatal(result)
	}
	for _, q := range []string{"-1", "bad", "", "1&snapshot=2"} {
		checkCode(t, req(a, "GET", "/api/admin/copies?snapshot="+q, "", session), 400)
	}
}
