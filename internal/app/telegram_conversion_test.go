package app

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	tgcore "github.com/M1saka10010/BanShit/internal/telegram"
)

type partialTelegram struct {
	fakeTelegram
	err error
}

func (f *partialTelegram) Collect(context.Context, string) ([]tgcore.Post, error) {
	return f.posts, f.err
}

func TestTelegramPostConversion(t *testing.T) {
	a := testApp(t)
	a.config.Telegram = tgcore.TelegramConfig{Enabled: true, APIID: 12345, APIHash: strings.Repeat("a", 32)}
	warning := errors.New("partial collection")
	post := tgcore.Post{ID: "memes/9", Text: "caption", Images: []string{"first", "second"}, Channel: "memes", URL: "https://t.me/memes/9", CreatedAt: "2026-01-01T00:00:00.000Z"}
	want := Meme{ID: post.ID, Text: post.Text, Images: post.Images, Channel: post.Channel, URL: post.URL, CreatedAt: post.CreatedAt}
	for _, tc := range []struct {
		name  string
		posts []tgcore.Post
		want  []Meme
		err   error
	}{
		{"success", []tgcore.Post{post}, []Meme{want}, nil},
		{"partial", []tgcore.Post{post}, []Meme{want}, warning},
		{"empty", []tgcore.Post{}, []Meme{}, nil},
		{"failure", nil, nil, warning},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a.tg = &partialTelegram{fakeTelegram: fakeTelegram{posts: tc.posts}, err: tc.err}
			got, err := a.collectTelegram(a.snapshot(), "memes")
			if err != tc.err || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %+v, %v; want %+v, %v", got, err, tc.want, tc.err)
			}
		})
	}
}
