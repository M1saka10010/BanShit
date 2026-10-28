package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	xdraw "golang.org/x/image/draw"
	"image"
	"image/draw"
	"image/jpeg"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

const mediaMaxBytes = 4 << 20
const albumMaxBytes = 20 << 20

var mediaRefPattern = regexp.MustCompile(`^media:([a-f0-9]{64})[.](jpeg|png|webp)$`)

type mediaContextKey struct{}
type mediaStore struct {
	dir string
	mu  sync.Mutex
}

func (a *App) aiContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, mediaContextKey{}, a.media)
}

func (s *mediaStore) read(ref string) ([]byte, string, error) {
	m := mediaRefPattern.FindStringSubmatch(ref)
	if s == nil || m == nil {
		return nil, "", errors.New("无效图片引用")
	}
	root, err := os.OpenRoot(s.dir)
	if err != nil {
		return nil, "", err
	}
	defer root.Close()
	f, err := root.Open(strings.TrimPrefix(ref, "media:"))
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, mediaMaxBytes+1))
	if err != nil {
		return nil, "", err
	}
	if len(b) > mediaMaxBytes || fmt.Sprintf("%x", sha256.Sum256(b)) != m[1] {
		return nil, "", errors.New("图片校验失败")
	}
	return b, m[2], nil
}

func atomicMediaFile(path string, b []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".media-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func (s *mediaStore) storeImages(images []string) ([]string, error) {
	refs := make([]string, 0, len(images))
	total := 0
	for _, value := range images {
		var b []byte
		var format string
		var err error
		if mediaRefPattern.MatchString(value) {
			b, format, err = s.read(value)
		} else {
			header, encoded, ok := strings.Cut(value, ",")
			if !ok || len(encoded) > base64.StdEncoding.EncodedLen(mediaMaxBytes) {
				return nil, errors.New("图片超过4 MiB或格式无效")
			}
			switch header {
			case "data:image/jpeg;base64":
				format = "jpeg"
			case "data:image/png;base64":
				format = "png"
			case "data:image/webp;base64":
				format = "webp"
			default:
				return nil, errors.New("仅支持JPEG PNG WebP")
			}
			b, err = base64.StdEncoding.DecodeString(encoded)
		}
		if err != nil {
			return nil, err
		}
		total += len(b)
		if len(b) > mediaMaxBytes || total > albumMaxBytes {
			return nil, errors.New("图片或相册超过大小限制")
		}
		cfg, detected, err := image.DecodeConfig(bytes.NewReader(b))
		if err != nil || detected != format || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > aiPreviewMaxPixels/cfg.Height {
			return nil, errors.New("图片格式或像素上限无效")
		}
		if s == nil {
			return nil, errors.New("图片存储未初始化")
		}
		ref := fmt.Sprintf("media:%x.%s", sha256.Sum256(b), format)
		s.mu.Lock()
		err = s.writeImage(ref, b, cfg)
		s.mu.Unlock()
		if err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

func (s *mediaStore) writeImage(ref string, b []byte, cfg image.Config) error {
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return err
	}
	path := filepath.Join(s.dir, strings.TrimPrefix(ref, "media:"))
	if _, _, err := s.read(ref); err == nil {
		if _, err = os.Stat(path + ".thumb.jpeg"); err == nil {
			return nil
		}
	}
	source, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return err
	}
	w, h := cfg.Width, cfg.Height
	if longest := max(w, h); longest > 640 {
		w = max(1, w*640/longest)
		h = max(1, h*640/longest)
	}
	thumb := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(thumb, thumb.Bounds(), image.White, image.Point{}, draw.Src)
	xdraw.ApproxBiLinear.Scale(thumb, thumb.Bounds(), source, source.Bounds(), draw.Over, nil)
	var output bytes.Buffer
	if err = jpeg.Encode(&output, thumb, &jpeg.Options{Quality: 80}); err != nil {
		return err
	}
	if err = atomicMediaFile(path, b); err != nil {
		return err
	}
	if _, _, err = s.read(ref); err != nil {
		return err
	}
	return atomicMediaFile(path+".thumb.jpeg", output.Bytes())
}

func (s *mediaStore) migrate(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS media_refs (ref TEXT NOT NULL, memeId TEXT NOT NULL REFERENCES memes(id) ON DELETE CASCADE, PRIMARY KEY(ref,memeId));
CREATE TRIGGER IF NOT EXISTS media_refs_insert AFTER INSERT ON memes BEGIN
 INSERT OR IGNORE INTO media_refs SELECT value,NEW.id FROM json_each(NEW.images) WHERE value LIKE 'media:%'; END;
CREATE TRIGGER IF NOT EXISTS media_refs_update AFTER UPDATE OF images ON memes BEGIN
 DELETE FROM media_refs WHERE memeId=OLD.id;
 INSERT OR IGNORE INTO media_refs SELECT value,NEW.id FROM json_each(NEW.images) WHERE value LIKE 'media:%'; END;`)
	if err != nil {
		return err
	}
	var done string
	err = db.QueryRow("SELECT value FROM meta WHERE key='mediaFilesV1'").Scan(&done)
	if err == nil && done == "1" {
		return s.verifyFiles(db)
	}
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	last := int64(0)
	for {
		var row int64
		var raw, id string
		err = db.QueryRow("SELECT rowid,id,images FROM memes WHERE rowid>? ORDER BY rowid LIMIT 1", last).Scan(&row, &id, &raw)
		if err == sql.ErrNoRows {
			break
		}
		if err != nil {
			return err
		}
		images, err := decodeImages(raw)
		if err != nil {
			return err
		}
		refs, err := s.storeImages(images)
		if err != nil {
			return fmt.Errorf("迁移图片 id=%q rowid=%d: %w", id, row, err)
		}
		encoded, _ := json.Marshal(refs)
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err = tx.Exec("UPDATE memes SET images=? WHERE rowid=? AND images=?", string(encoded), row, raw); err != nil {
			tx.Rollback()
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
		last = row
	}
	_, err = db.Exec("INSERT OR REPLACE INTO meta(key,value) VALUES('mediaFilesV1','1')")
	return err
}

func (s *mediaStore) verifyFiles(db *sql.DB) error {
	rows, err := db.Query("SELECT DISTINCT ref FROM media_refs")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var ref string
		if err = rows.Scan(&ref); err != nil {
			return err
		}
		if !mediaRefPattern.MatchString(ref) {
			return errors.New("图片存储引用损坏")
		}
		for _, suffix := range []string{"", ".thumb.jpeg"} {
			stat, err := os.Stat(filepath.Join(s.dir, strings.TrimPrefix(ref, "media:")+suffix))
			if err != nil || !stat.Mode().IsRegular() || stat.Size() == 0 {
				return fmt.Errorf("图片存储缺失或损坏: %s%s", ref, suffix)
			}
		}
	}
	return rows.Err()
}

func resolveAIImage(ctx context.Context, value string) (string, error) {
	if !strings.HasPrefix(value, "media:") {
		return value, nil
	}
	s, _ := ctx.Value(mediaContextKey{}).(*mediaStore)
	b, format, err := s.read(value)
	if err != nil {
		return "", err
	}
	return "data:image/" + format + ";base64," + base64.StdEncoding.EncodeToString(b), nil
}
