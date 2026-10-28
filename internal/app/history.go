package app

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

func (a *App) copyHistory(w http.ResponseWriter, r *http.Request) {
	page := 1
	if raw := r.URL.Query().Get("page"); raw != "" {
		v, e := strconv.Atoi(raw)
		if e != nil || v < 1 || v > 1000000 {
			fail(w, 400, "页码无效")
			return
		}
		page = v
	}
	start, end := "", ""
	var startTime, endTime time.Time
	for _, key := range []string{"start", "end"} {
		if raw := r.URL.Query().Get(key); raw != "" {
			v, e := time.Parse(time.RFC3339Nano, raw)
			if e != nil {
				fail(w, 400, "时间必须为带时区的RFC3339格式")
				return
			}
			if key == "start" {
				start = v.UTC().Format(time.RFC3339Nano)
				startTime = v
			} else {
				end = v.UTC().Format(time.RFC3339Nano)
				endTime = v
			}
		}
	}
	if !startTime.IsZero() && !endTime.IsZero() && startTime.After(endTime) {
		fail(w, 400, "开始时间不能晚于结束时间")
		return
	}
	before := int64(0)
	if raw := r.URL.Query().Get("before"); raw != "" {
		var e error
		before, e = strconv.ParseInt(raw, 10, 64)
		if e != nil || before < 1 {
			fail(w, 400, "分页游标无效")
			return
		}
	}
	tx, e := a.db.Begin()
	if e != nil {
		fail(w, 500, "读取失败")
		return
	}
	defer tx.Rollback()
	var snapshot int64
	if raw, present := r.URL.Query()["snapshot"]; present {
		if len(raw) != 1 {
			fail(w, 400, "快照无效")
			return
		}
		snapshot, e = strconv.ParseInt(raw[0], 10, 64)
		if e != nil || snapshot < 0 {
			fail(w, 400, "快照无效")
			return
		}
	} else if e = tx.QueryRow("SELECT coalesce(max(id),0) FROM copies").Scan(&snapshot); e != nil {
		fail(w, 500, "读取失败")
		return
	}
	where := " WHERE c.id<=? AND (?=0 OR c.id<?) AND (?='' OR julianday(c.createdAt)>=julianday(?)) AND (?='' OR julianday(c.createdAt)<=julianday(?))"
	args := []any{snapshot, before, before, start, start, end, end}
	var totalRecords int
	if e = tx.QueryRow("SELECT count(*) FROM copies c JOIN memes m ON m.id=c.memeId"+where, args...).Scan(&totalRecords); e != nil {
		fail(w, 500, "读取失败")
		return
	}
	pages := (totalRecords + 4) / 5
	if pages > 0 && page > pages {
		page = pages
	}
	if pages == 0 {
		page = 1
	}
	rows, e := tx.Query("SELECT c.id,c.kind,c.imageIndex,c.visitor,c.createdAt,m.id,m.text,m.images,m.channel,m.url FROM copies c JOIN memes m ON m.id=c.memeId"+where+" ORDER BY julianday(c.createdAt) DESC,c.id DESC LIMIT 6 OFFSET ?", append(args, (page-1)*5)...)
	if e != nil {
		fail(w, 500, "操作记录读取失败")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	var next int64
	for rows.Next() {
		var id int64
		var index int
		var kind, visitor, created, memeID, text, raw, channel, url string
		if e = rows.Scan(&id, &kind, &index, &visitor, &created, &memeID, &text, &raw, &channel, &url); e != nil {
			fail(w, 500, "操作记录读取失败")
			return
		}
		if len(items) == 5 {
			next = items[4]["id"].(int64)
			break
		}
		images, err := decodeImages(raw)
		if err != nil {
			fail(w, 500, "图片数据读取失败")
			return
		}
		total := len(images)
		switch kind {
		case "image":
			if index < 0 || index >= len(images) {
				images = []string{}
			} else {
				images = []string{images[index]}
			}
		case "text":
			images = []string{}
		}
		hash := sha256.Sum256([]byte(visitor))
		images, _, err = publicImages(images)
		if err != nil {
			fail(w, 500, "图片存储引用无效")
			return
		}
		items = append(items, map[string]any{"id": id, "kind": kind, "imageIndex": index, "visitor": fmt.Sprintf("访客-%x", hash[:6]), "createdAt": created, "memeId": memeID, "text": text, "images": images, "imageCount": total, "channel": channel, "url": url})
	}
	if rows.Err() != nil {
		fail(w, 500, "操作记录读取失败")
		return
	}
	reply(w, 200, map[string]any{"items": items, "snapshot": snapshot, "nextBefore": next, "page": page, "pageSize": 5, "total": totalRecords, "totalPages": pages})
}

// deleteCopy removes one operation record before it feeds a later interest
// optimization round; the meme loses the copy-based retention bonus at once.
func (a *App) deleteCopy(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ID int64 `json:"id"`
	}
	if e := decode(w, r, &input); e != nil {
		fail(w, 400, e.Error())
		return
	}
	if input.ID < 1 {
		fail(w, 400, "记录ID无效")
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	res, e := a.db.Exec("DELETE FROM copies WHERE id=?", input.ID)
	if e != nil {
		fail(w, 500, "删除失败")
		return
	}
	n, e := res.RowsAffected()
	if e != nil {
		fail(w, 500, "删除失败")
		return
	}
	if n == 0 {
		fail(w, 404, "记录不存在")
		return
	}
	a.copyDeletionVersion++
	reply(w, 200, map[string]bool{"ok": true})
}
