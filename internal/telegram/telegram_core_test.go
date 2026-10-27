package telegram

import (
	"context"
	"errors"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestTelegramUnconfiguredAndLazy(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "unused")
	service, err := NewTelegramService(ctx, TelegramConfig{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	st, err := service.Status(ctx)
	if err != nil || st.State != "idle" {
		t.Fatalf("%+v %v", st, err)
	}
	if _, err = os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("unconfigured service created directory")
	}
	configured, err := NewTelegramService(ctx, TelegramConfig{APIID: 1, APIHash: strings.Repeat("a", 32)}, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer configured.Close()
	if configured.(*telegramService).client != nil {
		t.Fatal("constructor started network client")
	}
	info, _ := os.Stat(dir)
	if info.Mode().Perm() != 0700 {
		t.Fatal(info.Mode())
	}
}
func TestTelegramSessionAtomicPermissions(t *testing.T) {
	dir := t.TempDir()
	s := telegramSessionStorage{path: filepath.Join(dir, "session.json")}
	ctx := context.Background()
	for _, value := range []string{"one", "two"} {
		if err := s.StoreSession(ctx, []byte(value)); err != nil {
			t.Fatal(err)
		}
		data, err := s.LoadSession(ctx)
		if err != nil || string(data) != value {
			t.Fatalf("%s %v", data, err)
		}
	}
	info, _ := os.Stat(s.path)
	if info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode())
	}
	unrelated := filepath.Join(dir, "other")
	os.WriteFile(unrelated, []byte("keep"), 0600)
	if err := s.remove(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatal(entries)
	}
}
func TestTelegramSafeErrorsAndExpiry(t *testing.T) {
	if strings.Contains(telegramSafeError(errors.New("secret-phone-code-password")).Error(), "secret") {
		t.Fatal("credential leak")
	}
	err := telegramSafeError(&tgerr.Error{Code: 420, Type: "FLOOD_WAIT", Argument: 42})
	if !strings.Contains(err.Error(), "42") {
		t.Fatal(err)
	}
	service, _ := NewTelegramService(context.Background(), TelegramConfig{}, t.TempDir())
	defer service.Close()
	s := service.(*telegramService)
	s.state = "code"
	s.phone = "private"
	s.hash = "private"
	s.expires = time.Now().Add(-time.Second)
	st, err := s.SignIn(context.Background(), "private")
	if err == nil || st.State != "idle" || s.phone != "" || s.hash != "" {
		t.Fatalf("%+v %v", st, err)
	}
}
func TestTelegramGroupsAndConversion(t *testing.T) {
	photo := func(id int, group int64) *tg.Message {
		return &tg.Message{ID: id, GroupedID: group, Media: &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: int64(id)}}}
	}
	a, b, c := photo(12, 8), photo(11, 8), photo(10, 7)
	a.Message = "caption"
	complete := telegramMessageGroups([]tg.MessageClass{a, b}, false)
	if len(complete) != 1 || len(complete[0]) != 2 {
		t.Fatal("complete oldest album discarded")
	}
	groups := telegramMessageGroups([]tg.MessageClass{a, b, c}, true)
	if len(groups) != 1 || len(groups[0]) != 2 {
		t.Fatal(groups)
	}
	load := func(context.Context, *tg.Photo) ([]byte, error) { return []byte{255, 216, 255, 224, 0, 0}, nil }
	meme, ok, err := telegramConvertGroup(context.Background(), "CHANNEL", groups[0], load)
	if err != nil || !ok || meme.ID != "channel/11" || meme.Text != "caption" || len(meme.Images) != 2 || !strings.HasPrefix(meme.Images[0], "data:image/jpeg;base64,") {
		t.Fatalf("%+v %v %v", meme, ok, err)
	}
	b.Media = &tg.MessageMediaDocument{}
	_, ok, err = telegramConvertGroup(context.Background(), "channel", groups[0], load)
	if ok || err != nil {
		t.Fatal("video converted to text")
	}
	b.Media = &tg.MessageMediaPhoto{Photo: &tg.Photo{}}
	calls := 0
	_, ok, err = telegramConvertGroup(context.Background(), "channel", groups[0], func(context.Context, *tg.Photo) ([]byte, error) {
		calls++
		if calls == 2 {
			return nil, errors.New("failed")
		}
		return load(context.Background(), nil)
	})
	if ok || err == nil {
		t.Fatal("partial album emitted")
	}
}
func TestTelegramMediaLimits(t *testing.T) {
	w := &telegramLimitedWriter{limit: 3}
	if n, err := w.Write([]byte("abc")); n != 3 || err != nil {
		t.Fatal(n, err)
	}
	if n, err := w.Write([]byte("d")); n != 0 || err == nil || w.Len() != 3 {
		t.Fatal("unbounded writer")
	}
	group := make([]*tg.Message, 6)
	for i := range group {
		group[i] = &tg.Message{ID: i + 1, Media: &tg.MessageMediaPhoto{Photo: &tg.Photo{}}}
	}
	_, ok, err := telegramConvertGroup(context.Background(), "channel", group, func(context.Context, *tg.Photo) ([]byte, error) {
		b := make([]byte, telegramPhotoLimit)
		copy(b, []byte{255, 216, 255, 224})
		return b, nil
	})
	if ok || err != nil {
		t.Fatal("album over limit")
	}
	_, ok, err = telegramConvertGroup(context.Background(), "channel", group[:1], func(context.Context, *tg.Photo) ([]byte, error) { return make([]byte, telegramPhotoLimit+1), nil })
	if ok || err != nil {
		t.Fatal("photo over limit")
	}
}

func TestTelegramConcurrentCloseAndStatus(t *testing.T) {
	service, err := NewTelegramService(context.Background(), TelegramConfig{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			service.Status(context.Background())
			service.SignIn(context.Background(), "12345")
			service.Close()
		}()
	}
	wg.Wait()
}

func TestTelegramRecentAlbumAndPlainText(t *testing.T) {
	recent := &tg.Message{ID: 1, GroupedID: 7, Date: int(time.Now().Unix()), Message: "pending"}
	if _, ok, _ := telegramConvertGroup(context.Background(), "channel", []*tg.Message{recent}, nil); ok {
		t.Fatal("recent partial album")
	}
	recent.GroupedID = 0
	m, ok, err := telegramConvertGroup(context.Background(), "channel", []*tg.Message{recent}, nil)
	if err != nil || !ok || m.Text != "pending" {
		t.Fatalf("%+v %v", m, err)
	}
}
