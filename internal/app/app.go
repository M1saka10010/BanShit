package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	tgcore "github.com/M1saka10010/BanShit/internal/telegram"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type Meme struct {
	ID         string   `json:"id"`
	Text       string   `json:"text"`
	Images     []string `json:"images"`
	Thumbnails []string `json:"thumbnails,omitempty"`
	Channel    string   `json:"channel"`
	URL        string   `json:"url"`
	Score      float64  `json:"score"`
	CreatedAt  string   `json:"createdAt"`
}
type Status struct {
	Running          bool   `json:"running"`
	LastRun          string `json:"lastRun"`
	LastError        string `json:"lastError"`
	LastOptimization string `json:"lastOptimization"`
}
type limit struct {
	Count int
	Until time.Time
}
type App struct {
	tgMu                      sync.Mutex
	tg                        tgcore.TelegramService
	tgDir                     string
	tgFactory                 func(context.Context, tgcore.TelegramConfig, string) (tgcore.TelegramService, error)
	mu                        sync.Mutex
	config                    Config
	configPath                string
	db                        *sql.DB
	media                     *mediaStore
	client                    *http.Client
	status                    Status
	sessions                  map[string]time.Time
	limits                    map[string]limit
	lastRefresh, lastOptimize time.Time
	ctx                       context.Context
	cancel                    context.CancelFunc
	wg                        sync.WaitGroup
}

func NewApp(configPath, dataPath string) (*App, error) {
	b, e := os.ReadFile(configPath)
	if e != nil {
		return nil, fmt.Errorf("请从 config.example.json 创建配置文件: %w", e)
	}
	var c Config
	if e = json.Unmarshal(b, &c); e != nil {
		return nil, e
	}
	if e = validate(&c); e != nil {
		return nil, e
	}
	if len(c.AdminPassword) < 12 || strings.HasPrefix(c.AdminPassword, "CHANGE-ME") {
		return nil, errors.New("请设置至少 12 位的管理密码")
	}
	if c.Port == 0 {
		c.Port = 3000
	}
	if c.Port < 1 || c.Port > 65535 {
		return nil, errors.New("端口无效")
	}
	if c.Host == "" {
		c.Host = "127.0.0.1"
	}
	if e = os.Chmod(configPath, 0600); e != nil {
		return nil, e
	}
	if e = os.MkdirAll(dataPath, 0700); e != nil {
		return nil, e
	}
	db, e := sql.Open("sqlite", filepath.Join(dataPath, "banshit.sqlite"))
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	_, e = db.Exec("PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000; CREATE TABLE IF NOT EXISTS memes (id TEXT PRIMARY KEY,text TEXT NOT NULL,imageData TEXT NOT NULL,channel TEXT NOT NULL,url TEXT NOT NULL,score REAL NOT NULL,createdAt TEXT NOT NULL); CREATE TABLE IF NOT EXISTS copies (id INTEGER PRIMARY KEY,memeId TEXT NOT NULL REFERENCES memes(id),kind TEXT NOT NULL,visitor TEXT NOT NULL,createdAt TEXT NOT NULL); CREATE INDEX IF NOT EXISTS copies_meme ON copies(memeId); CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY,value TEXT NOT NULL);")
	if e != nil {
		db.Close()
		return nil, e
	}
	if e = migrateMedia(db); e != nil {
		db.Close()
		return nil, e
	}
	if e = migrateFeed(db); e != nil {
		db.Close()
		return nil, e
	}
	media := &mediaStore{dir: filepath.Join(dataPath, "media")}
	if e = media.migrate(db); e != nil {
		db.Close()
		return nil, e
	}
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), mediaContextKey{}, media))
	a := &App{config: c, configPath: configPath, db: db, ctx: ctx, cancel: cancel, client: &http.Client{Timeout: 90 * time.Second}, sessions: map[string]time.Time{}, limits: map[string]limit{}}
	a.status.LastRun = a.meta("lastRun")
	a.media = media
	a.status.LastOptimization = a.meta("lastOptimization")
	a.lastOptimize, _ = time.Parse(time.RFC3339, a.status.LastOptimization)
	a.tgDir = filepath.Join(dataPath, "telegram")
	a.tgFactory = tgcore.NewTelegramService
	return a, nil
}
func (a *App) Close() {
	a.cancel()
	a.wg.Wait()
	a.tgMu.Lock()
	if a.tg != nil {
		a.tg.Close()
	}
	a.tgMu.Unlock()
	a.db.Close()
}
func (a *App) meta(key string) string {
	var s string
	_ = a.db.QueryRow("SELECT value FROM meta WHERE key=?", key).Scan(&s)
	return s
}
func (a *App) setMeta(key, value string) error {
	_, e := a.db.Exec("INSERT OR REPLACE INTO meta VALUES (?,?)", key, value)
	return e
}
