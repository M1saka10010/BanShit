package app

import (
	"database/sql"
	"testing"
)

func TestMigrateCopyIDs(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`PRAGMA foreign_keys=ON;
CREATE TABLE memes(id TEXT PRIMARY KEY); INSERT INTO memes VALUES('m');
CREATE TABLE meta(key TEXT PRIMARY KEY,value TEXT NOT NULL); INSERT INTO meta VALUES('optimizedCopyId','100');
CREATE TABLE copies(id INTEGER PRIMARY KEY,memeId TEXT NOT NULL REFERENCES memes(id),kind TEXT NOT NULL,visitor TEXT NOT NULL,createdAt TEXT NOT NULL,imageIndex INTEGER NOT NULL DEFAULT -1);
CREATE INDEX copies_meme ON copies(memeId);
INSERT INTO copies VALUES(8,'m','image','visitor','date',2);`)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = migrateCopyIDs(db); err != nil {
			t.Fatal(err)
		}
	}
	var kind, visitor, date string
	var index int
	if err = db.QueryRow("SELECT kind,visitor,createdAt,imageIndex FROM copies WHERE id=8").Scan(&kind, &visitor, &date, &index); err != nil {
		t.Fatal(err)
	}
	if kind != "image" || visitor != "visitor" || date != "date" || index != 2 {
		t.Fatal("migration changed record")
	}
	insert := func() int64 {
		t.Helper()
		r, e := db.Exec("INSERT INTO copies(memeId,kind,visitor,createdAt) VALUES('m','text','v','now')")
		if e != nil {
			t.Fatal(e)
		}
		id, e := r.LastInsertId()
		if e != nil {
			t.Fatal(e)
		}
		return id
	}
	if id := insert(); id != 101 {
		t.Fatalf("id=%d want 101", id)
	}
	if _, err = db.Exec("DELETE FROM copies"); err != nil {
		t.Fatal(err)
	}
	if err = migrateCopyIDs(db); err != nil {
		t.Fatal(err)
	}
	if id := insert(); id != 102 {
		t.Fatalf("id reused: %d", id)
	}
	if _, err = db.Exec("INSERT INTO copies(memeId,kind,visitor,createdAt) VALUES('missing','text','v','now')"); err == nil {
		t.Fatal("foreign key lost")
	}
	var n int
	if err = db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='index' AND name='copies_meme'").Scan(&n); err != nil || n != 1 {
		t.Fatal("index lost", err)
	}
}
