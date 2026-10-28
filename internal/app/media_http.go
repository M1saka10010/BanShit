package app

import (
	"errors"
	"net/http"
	"os"
	"strings"
	"time"
)

func publicImages(refs []string) ([]string, []string, error) {
	images, thumbs := make([]string, 0, len(refs)), make([]string, 0, len(refs))
	for _, ref := range refs {
		if !mediaRefPattern.MatchString(ref) {
			return nil, nil, errors.New("图片存储引用无效")
		}
		url := "/media/" + strings.TrimPrefix(ref, "media:")
		images = append(images, url)
		thumbs = append(thumbs, url+"/thumb")
	}
	return images, thumbs, nil
}

func (a *App) serveMedia(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-cache")
	name := strings.TrimPrefix(r.URL.Path, "/media/")
	thumb := strings.HasSuffix(name, "/thumb")
	if thumb {
		name = strings.TrimSuffix(name, "/thumb")
	}
	ref := "media:" + name
	match := mediaRefPattern.FindStringSubmatch(ref)
	if match == nil {
		fail(w, 404, "未找到")
		return
	}
	a.mu.Lock()
	admin := time.Now().Before(a.sessions[cookie(r, "session")])
	a.mu.Unlock()
	args := []any{ref}
	query := "SELECT 1 FROM media_refs i JOIN memes m ON m.id=i.memeId WHERE i.ref=?"
	if !admin {
		channels := a.snapshot().Channels
		if len(channels) == 0 {
			fail(w, 404, "未找到")
			return
		}
		query += " AND m.channel IN (" + strings.TrimSuffix(strings.Repeat("?,", len(channels)), ",") + ")"
		for _, channel := range channels {
			args = append(args, channel)
		}
	}
	var exists int
	if err := a.db.QueryRow(query+" LIMIT 1", args...).Scan(&exists); err != nil {
		fail(w, 404, "未找到")
		return
	}
	mime := "image/" + match[2]
	etag := `"` + name + `"`
	if thumb {
		name += ".thumb.jpeg"
		mime = "image/jpeg"
		etag = `"` + name + `"`
	}
	if a.media == nil {
		fail(w, 404, "未找到")
		return
	}
	root, err := os.OpenRoot(a.media.dir)
	if err != nil {
		fail(w, 404, "未找到")
		return
	}
	defer root.Close()
	f, err := root.Open(name)
	if err != nil {
		fail(w, 404, "未找到")
		return
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil || !stat.Mode().IsRegular() {
		fail(w, 404, "未找到")
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("ETag", etag)
	http.ServeContent(w, r, name, time.Time{}, f)
}
