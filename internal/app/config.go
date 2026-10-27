package app

import (
	"encoding/json"
	"errors"
	"fmt"
	tgcore "github.com/M1saka10010/BanShit/internal/telegram"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type AIConfig struct {
	BaseURL string `json:"baseUrl"`
	Mode    string `json:"mode"`
	Model   string `json:"model"`
	APIKey  string `json:"apiKey,omitempty"`
}
type Config struct {
	Telegram           tgcore.TelegramConfig `json:"telegram"`
	Host               string                `json:"host"`
	Port               int                   `json:"port"`
	AdminPassword      string                `json:"adminPassword"`
	SecureCookie       bool                  `json:"secureCookie"`
	TrustedProxies     []string              `json:"trustedProxies"`
	Channels           []string              `json:"channels"`
	IntervalMinutes    float64               `json:"intervalMinutes"`
	OptimizeEveryHours float64               `json:"optimizeEveryHours"`
	InterestPrompt     string                `json:"interestPrompt"`
	AI                 AIConfig              `json:"ai"`
}

var channelPattern = regexp.MustCompile("^[a-zA-Z0-9_]{5,32}$")

func validate(c *Config) error {
	if e := tgcore.ValidateTelegramConfig(c.Telegram); e != nil {
		return e
	}
	if e := validateTrustedProxies(c.TrustedProxies); e != nil {
		return e
	}
	if len(c.Channels) > 30 {
		return errors.New("最多 30 个频道")
	}
	channels := []string{}
	seen := map[string]bool{}
	for _, s := range c.Channels {
		s = strings.TrimSpace(s)
		s = strings.TrimPrefix(s, "https://t.me/")
		s = strings.TrimPrefix(s, "s/")
		s = strings.TrimPrefix(s, "@")
		s = strings.TrimSuffix(s, "/")
		s = strings.ToLower(s)
		if !channelPattern.MatchString(s) {
			return fmt.Errorf("无效频道: %s", s)
		}
		if !seen[s] {
			channels = append(channels, s)
			seen[s] = true
		}
	}
	c.Channels = channels
	if c.IntervalMinutes < 1 || c.IntervalMinutes > 10080 || c.OptimizeEveryHours < 1 || c.OptimizeEveryHours > 720 {
		return errors.New("更新周期需为 1–10080 分钟，优化周期为 1–720 小时")
	}
	if len(c.InterestPrompt) > 36000 || len(c.AI.Model) > 200 {
		return errors.New("提示词或模型名称过长")
	}
	if c.AI.Mode != "chat" && c.AI.Mode != "responses" {
		return errors.New("协议必须为 chat 或 responses")
	}
	u, e := url.Parse(c.AI.BaseURL)
	if e != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("API 地址无效")
	}
	return nil
}
func (a *App) snapshot() Config { a.mu.Lock(); defer a.mu.Unlock(); return a.config }

// Caller holds mu; same-directory rename keeps config changes atomic.
func (a *App) save(c Config) error {
	b, e := json.MarshalIndent(c, "", "  ")
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(a.configPath), ".banshit-config-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if _, e = f.Write(append(b, '\n')); e != nil {
		f.Close()
		return e
	}
	if e = f.Sync(); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	if e = os.Rename(f.Name(), a.configPath); e != nil {
		return e
	}
	a.config = c
	return nil
}
