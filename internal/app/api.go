package app

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

func (a *App) admin(w http.ResponseWriter, r *http.Request) {
	c := a.snapshot()
	switch {
	case strings.HasPrefix(r.URL.Path, "/api/admin/telegram"):
		a.telegramAdmin(w, r)
	case r.URL.Path == "/api/admin/copies" && r.Method == "GET":
		a.copyHistory(w, r)
	case r.URL.Path == "/api/admin/copies" && r.Method == "DELETE":
		a.deleteCopy(w, r)
	case r.URL.Path == "/api/admin/config" && r.Method == "GET":
		var memes, copies int
		if e := a.db.QueryRow("SELECT count(*) FROM memes").Scan(&memes); e != nil {
			fail(w, 500, "数据库读取失败")
			return
		}
		if e := a.db.QueryRow("SELECT count(*) FROM copies").Scan(&copies); e != nil {
			fail(w, 500, "数据库读取失败")
			return
		}
		a.mu.Lock()
		s := a.status
		a.mu.Unlock()
		reply(w, 200, map[string]any{"channels": c.Channels, "intervalMinutes": c.IntervalMinutes, "optimizeEveryHours": c.OptimizeEveryHours, "interestPrompt": c.InterestPrompt, "ai": map[string]any{"baseUrl": c.AI.BaseURL, "mode": c.AI.Mode, "model": c.AI.Model, "hasApiKey": c.AI.APIKey != "", "concurrency": c.AI.Concurrency}, "status": s, "stats": map[string]int{"memes": memes, "copies": copies}})
	case r.URL.Path == "/api/admin/config" && r.Method == "PUT":
		var next Config
		if e := decode(w, r, &next); e != nil {
			fail(w, 400, e.Error())
			return
		}
		if e := validate(&next); e != nil {
			fail(w, 400, e.Error())
			return
		}
		a.mu.Lock()
		next.Host = a.config.Host
		next.Port = a.config.Port
		next.AdminPassword = a.config.AdminPassword
		next.SecureCookie = a.config.SecureCookie
		next.TrustedProxies = a.config.TrustedProxies
		next.Telegram = a.config.Telegram
		if next.AI.APIKey == "" {
			next.AI.APIKey = a.config.AI.APIKey
		}
		e := a.save(next)
		a.mu.Unlock()
		if e != nil {
			fail(w, 500, "保存配置失败")
			return
		}
		reply(w, 200, map[string]bool{"ok": true})
	case r.URL.Path == "/api/admin/models" && r.Method == "POST":
		ai := c.AI
		ai.APIKey = ""
		if e := decode(w, r, &ai); e != nil {
			fail(w, 400, e.Error())
			return
		}
		candidate := c
		candidate.AI = ai
		if e := validate(&candidate); e != nil {
			fail(w, 400, e.Error())
			return
		}
		if ai.APIKey == "" && Endpoint(ai.BaseURL, "models") == Endpoint(c.AI.BaseURL, "models") {
			ai.APIKey = c.AI.APIKey
		}
		headers := map[string]string{}
		if ai.APIKey != "" {
			headers["Authorization"] = "Bearer " + ai.APIKey
		}
		b, _, e := FetchLimited(r.Context(), a.client, Endpoint(ai.BaseURL, "models"), headers, nil, 8<<20)
		if e != nil {
			fail(w, 502, e.Error())
			return
		}
		var result struct{ Data []map[string]any }
		if e = json.Unmarshal(b, &result); e != nil || result.Data == nil {
			fail(w, 502, "模型列表缺少 data 数组")
			return
		}
		models := []map[string]string{}
		for _, m := range result.Data {
			if id, ok := m["id"].(string); ok {
				models = append(models, map[string]string{"id": id})
			}
		}
		reply(w, 200, map[string]any{"models": models})
	case r.URL.Path == "/api/admin/reevaluate" && r.Method == "POST":
		if !a.launchReevaluate() {
			fail(w, 409, "已有任务正在执行或服务正在关闭")
			return
		}
		reply(w, 202, map[string]bool{"ok": true})
	case (r.URL.Path == "/api/admin/refresh" || r.URL.Path == "/api/admin/optimize") && r.Method == "POST":
		if !a.launch(strings.HasSuffix(r.URL.Path, "optimize")) {
			fail(w, 409, "已有任务正在执行或服务正在关闭")
			return
		}
		reply(w, 202, map[string]bool{"ok": true})
	default:
		fail(w, 404, "未找到")
	}
}

type scanner interface{ Scan(...any) error }

func scanMeme(s scanner) (Meme, error) {
	var m Meme
	var raw string
	e := s.Scan(&m.ID, &m.Text, &raw, &m.Channel, &m.URL, &m.Score, &m.CreatedAt)
	if e == nil {
		m.Images, e = decodeImages(raw)
	}
	return m, e
}
func (a *App) feed(w http.ResponseWriter, r *http.Request) {
	v := cookie(r, "visitor")
	if len(v) != 32 {
		a.setCookie(w, "visitor", token(16), 31536000)
	}
	memes, updated, err := a.feedSnapshot()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	reply(w, 200, map[string]any{"memes": memes, "updatedAt": updated})
}

func (a *App) feedSnapshot() ([]Meme, string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	c := a.config
	memes := []Meme{}
	if len(c.Channels) > 0 {
		args := []any{}
		for _, s := range c.Channels {
			args = append(args, s)
		}
		q := "SELECT m.* FROM feed f JOIN memes m ON m.id=f.memeId WHERE m.channel IN (" + strings.TrimSuffix(strings.Repeat("?,", len(args)), ",") + ") ORDER BY f.position LIMIT 16"
		rows, e := a.db.Query(q, args...)
		if e != nil {
			return nil, "", errors.New("数据库读取失败")
		}
		defer rows.Close()
		for rows.Next() {
			m, e := scanMeme(rows)
			if e != nil {
				return nil, "", errors.New("数据库读取失败")
			}
			m.Images, m.Thumbnails, e = publicImages(m.Images)
			if e != nil {
				return nil, "", errors.New("图片存储引用无效")
			}
			memes = append(memes, m)
		}
		if rows.Err() != nil {
			return nil, "", errors.New("数据库读取失败")
		}
	}
	return memes, a.status.LastRun, nil
}
func (a *App) copyMeme(w http.ResponseWriter, r *http.Request) {
	if a.limited("copy:"+a.clientIP(r), 60, time.Minute) {
		fail(w, 429, "复制反馈过于频繁")
		return
	}
	visitor := cookie(r, "visitor")
	if len(visitor) != 32 {
		fail(w, 400, "请先加载首页")
		return
	}
	var input struct {
		Kind       string
		ImageIndex *int `json:"imageIndex"`
	}
	if e := decode(w, r, &input); e != nil {
		fail(w, 400, e.Error())
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/memes/"), "/copy")
	// Serialize recording with pruning so a successful copy is never discarded.
	a.mu.Lock()
	defer a.mu.Unlock()
	m, e := scanMeme(a.db.QueryRow("SELECT * FROM memes WHERE id=?", id))
	if e == sql.ErrNoRows {
		fail(w, 404, "内容不存在")
		return
	}
	if e != nil {
		fail(w, 500, "数据库读取失败")
		return
	}
	index := -1
	if input.Kind == "image" {
		if input.ImageIndex != nil {
			index = *input.ImageIndex
		} else if len(m.Images) == 1 {
			index = 0
		}
	}
	if (input.Kind != "text" && input.Kind != "image" && input.Kind != "album") || (input.Kind == "image" && (index < 0 || index >= len(m.Images))) || (input.Kind == "album" && len(m.Images) == 0) || (input.Kind == "text" && m.Text == "") || (input.Kind != "image" && input.ImageIndex != nil) {
		fail(w, 400, "复制类型无效")
		return
	}
	_, e = a.db.Exec("INSERT INTO copies (memeId,kind,visitor,createdAt,imageIndex) VALUES(?,?,?,?,?)", id, input.Kind, visitor, time.Now().UTC().Format(time.RFC3339), index)
	if e != nil {
		fail(w, 500, "保存复制反馈失败")
		return
	}
	reply(w, 200, map[string]bool{"ok": true})
}
