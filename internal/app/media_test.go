package app

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestMediaMigration(t *testing.T) {
	db, e := sql.Open("sqlite", filepath.Join(t.TempDir(), "old.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	_, e = db.Exec("CREATE TABLE memes(id TEXT PRIMARY KEY,text TEXT,imageData TEXT,channel TEXT,url TEXT,score REAL,createdAt TEXT); CREATE TABLE copies(id INTEGER PRIMARY KEY,memeId TEXT,kind TEXT,visitor TEXT,createdAt TEXT); INSERT INTO memes VALUES('a','text','data:image/png;base64,YQ==','memes','',1,''); INSERT INTO memes VALUES('b','text','','memes','',1,''); INSERT INTO copies VALUES(1,'a','image','v','')")
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2; i++ {
		if e = migrateMedia(db); e != nil {
			t.Fatal(e)
		}
	}
	for _, id := range []string{"a", "b"} {
		m, e := scanMeme(db.QueryRow("SELECT * FROM memes WHERE id=?", id))
		if e != nil {
			t.Fatal(e)
		}
		want := 0
		if id == "a" {
			want = 1
		}
		if len(m.Images) != want {
			t.Fatal(m)
		}
	}
	var index int
	if e = db.QueryRow("SELECT imageIndex FROM copies WHERE id=1").Scan(&index); e != nil || index != 0 {
		t.Fatal(index, e)
	}
}
func TestMediaCopies(t *testing.T) {
	a := testApp(t)
	for _, m := range []Meme{{ID: "memes/1", Text: "pure text", Channel: "memes"}, {ID: "memes/2", Images: []string{"data:image/png;base64,YQ==", "data:image/png;base64,Yg=="}, Channel: "memes"}} {
		if e := a.storeMeme(m); e != nil {
			t.Fatal(e)
		}
	}
	pool, err := a.rankingPool(a.snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if err = a.publishFeed(a.snapshot(), pool); err != nil {
		t.Fatal(err)
	}
	w := req(a, "GET", "/api/memes", "")
	visitor := w.Result().Cookies()[0]
	var feed struct{ Memes []Meme }
	if e := json.Unmarshal(w.Body.Bytes(), &feed); e != nil {
		t.Fatal(e)
	}
	if len(feed.Memes) != 2 {
		t.Fatal(feed)
	}
	for _, tc := range []struct {
		id, body string
		code     int
	}{{"1", "{\"kind\":\"text\"}", 200}, {"1", "{\"kind\":\"album\"}", 400}, {"1", "{\"kind\":\"image\",\"imageIndex\":0}", 400}, {"2", "{\"kind\":\"text\"}", 400}, {"2", "{\"kind\":\"image\"}", 400}, {"2", "{\"kind\":\"image\",\"imageIndex\":2}", 400}, {"2", "{\"kind\":\"image\",\"imageIndex\":0}", 200}, {"2", "{\"kind\":\"image\",\"imageIndex\":1}", 200}, {"2", "{\"kind\":\"image\",\"imageIndex\":1}", 200}, {"2", "{\"kind\":\"album\"}", 200}} {
		checkCode(t, req(a, "POST", "/api/memes/memes%2F"+tc.id+"/copy", tc.body, visitor), tc.code)
	}
	var n int
	a.db.QueryRow("SELECT count(*) FROM copies").Scan(&n)
	if n != 5 {
		t.Fatal(n)
	}
	m, e := scanMeme(a.db.QueryRow("SELECT * FROM memes WHERE id='memes/2'"))
	if e != nil || len(m.Images) != 2 {
		t.Fatal(m, e)
	}
}
