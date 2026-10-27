package app

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"
)

func migrateFeed(db *sql.DB) error {
	tx, e := db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.Exec("CREATE TABLE IF NOT EXISTS feed (position INTEGER PRIMARY KEY, memeId TEXT NOT NULL UNIQUE REFERENCES memes(id))"); e != nil {
		return e
	}
	var marker string
	e = tx.QueryRow("SELECT value FROM meta WHERE key='feedInitialized'").Scan(&marker)
	if e == sql.ErrNoRows {
		// Preserve the old homepage during the first ranking run.
		if _, e = tx.Exec("INSERT INTO feed(position,memeId) SELECT row_number() OVER (ORDER BY score DESC,createdAt DESC),id FROM (SELECT * FROM memes ORDER BY createdAt DESC LIMIT 100) ORDER BY score DESC,createdAt DESC LIMIT 16; INSERT INTO meta(key,value) VALUES('feedInitialized','1')"); e != nil {
			return e
		}
	} else if e != nil {
		return e
	}
	return tx.Commit()
}
func (a *App) rankingPool(c Config) ([]Meme, error) {
	result := []Meme{}
	if len(c.Channels) == 0 {
		return result, nil
	}
	args := []any{}
	for _, s := range c.Channels {
		args = append(args, s)
	}
	rows, e := a.db.Query("SELECT * FROM memes WHERE channel IN ("+strings.TrimSuffix(strings.Repeat("?,", len(args)), ",")+") ORDER BY createdAt DESC,id DESC LIMIT 100", args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		m, e := scanMeme(rows)
		if e != nil {
			return nil, e
		}
		result = append(result, m)
	}
	return result, rows.Err()
}
func (a *App) publishFeed(c Config, ranked []Meme, identity ...string) error {
	if len(ranked) > 16 {
		return errors.New("首页最多16条")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if !reflect.DeepEqual(c.Channels, a.config.Channels) || c.InterestPrompt != a.config.InterestPrompt || c.AI != a.config.AI || c.Telegram != a.config.Telegram {
		return errors.New("排序期间配置已变更，保留上一版首页")
	}
	tx, e := a.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.Exec("DELETE FROM feed"); e != nil {
		return e
	}
	for i, m := range ranked {
		if _, e = tx.Exec("INSERT INTO feed(position,memeId) VALUES(?,?)", i+1, m.ID); e != nil {
			return e
		}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, e = tx.Exec("INSERT OR REPLACE INTO meta(key,value) VALUES('lastRun',?)", now); e != nil {
		return e
	}
	if len(identity) > 0 {
		if _, e = tx.Exec("INSERT OR REPLACE INTO meta(key,value) VALUES('rankingIdentity',?)", identity[0]); e != nil {
			return e
		}
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	a.status.LastRun = now
	return nil
}
func rankingIdentity(c Config, pool []Meme) string {
	ids := []string{}
	for _, m := range pool {
		ids = append(ids, m.ID)
	}
	b, _ := json.Marshal(struct {
		IDs      []string
		Interest string
		AI       AIConfig
	}{ids, c.InterestPrompt, c.AI})
	return fmt.Sprintf("%x", sha256.Sum256(b))
}
