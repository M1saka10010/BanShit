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
func (a *App) publishedCandidates(c Config) ([]Meme, error) {
	rows, err := a.db.Query("SELECT m.* FROM feed f JOIN memes m ON m.id=f.memeId ORDER BY f.position")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	channels := map[string]bool{}
	for _, channel := range c.Channels {
		channels[channel] = true
	}
	var result []Meme
	for rows.Next() {
		m, err := scanMeme(rows)
		if err != nil {
			return nil, err
		}
		if channels[m.Channel] {
			result = append(result, m)
		}
	}
	return result, rows.Err()
}

func (a *App) publishFeed(c Config, ranked []Meme, identity ...string) error {
	if len(ranked) > 16 {
		return errors.New("首页最多16条")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ctx.Err(); err != nil {
		return err
	}
	if !reflect.DeepEqual(c.Channels, a.config.Channels) || rankingPolicy(c) != rankingPolicy(a.config) {
		return errors.New("排序期间配置已变更，保留上一版首页")
	}
	tx, e := a.db.BeginTx(a.ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	rows, e := tx.Query("SELECT memeId FROM feed ORDER BY position")
	if e != nil {
		return e
	}
	var current []string
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
		current = append(current, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	changed := len(current) != len(ranked)
	for i, m := range ranked {
		if i >= len(current) || current[i] != m.ID {
			changed = true
		}
	}
	if changed {
		if _, e = tx.Exec("DELETE FROM feed"); e != nil {
			return e
		}
		for i, m := range ranked {
			if _, e = tx.Exec("INSERT INTO feed(position,memeId) VALUES(?,?)", i+1, m.ID); e != nil {
				return e
			}
		}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if changed {
		if _, e = tx.Exec("INSERT OR REPLACE INTO meta(key,value) VALUES('lastRun',?)", now); e != nil {
			return e
		}
	}
	if len(identity) > 0 {
		if _, e = tx.Exec("INSERT OR REPLACE INTO meta(key,value) VALUES('rankingIdentity',?)", identity[0]); e != nil {
			return e
		}
	}
	if len(identity) > 1 {
		if _, e = tx.Exec("INSERT OR REPLACE INTO meta(key,value) VALUES('rankingState',?)", identity[1]); e != nil {
			return e
		}
		if _, e = tx.Exec("DELETE FROM meta WHERE key='rankingPending'"); e != nil {
			return e
		}
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	if changed {
		a.status.LastRun = now
	}
	return nil
}
func rankingIdentity(c Config, pool []Meme) string {
	ids := []string{}
	for _, m := range pool {
		ids = append(ids, fmt.Sprintf("%s:%x", m.ID, rankFingerprint(m)))
	}
	b, _ := json.Marshal(struct {
		IDs      []string
		Interest string
		Policy   string
	}{ids, c.InterestPrompt, rankingPolicy(c)})
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

func rankingPolicy(c Config) string {
	b, _ := json.Marshal([]string{"admission-top16-v3", rankInstruction(c), admissionInstruction(c), c.AI.BaseURL, c.AI.Model, c.AI.Mode})
	return fmt.Sprintf("%x", sha256.Sum256(b))
}
