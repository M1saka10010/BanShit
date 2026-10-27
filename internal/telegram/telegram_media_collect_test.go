package telegram

import (
	"context"
	"errors"
	"fmt"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"strings"
	"testing"
	"time"
)

type mediaMockPool struct {
	calls, closed int
	fail          map[int]error
}

func (p *mediaMockPool) Close() error { p.closed++; return nil }
func (p *mediaMockPool) Invoke(ctx context.Context, in bin.Encoder, out bin.Decoder) error {
	p.calls++
	if err := p.fail[p.calls]; err != nil {
		return err
	}
	b := &bin.Buffer{}
	if err := (&tg.UploadFile{Type: &tg.StorageFileJpeg{}, Bytes: []byte{255, 216, 255, 224, 0, 0}}).Encode(b); err != nil {
		return err
	}
	return out.Decode(b)
}

type mediaMockAPI struct {
	messages               []tg.MessageClass
	resolveErr, historyErr error
}

func (a mediaMockAPI) ContactsResolveUsername(context.Context, *tg.ContactsResolveUsernameRequest) (*tg.ContactsResolvedPeer, error) {
	return &tg.ContactsResolvedPeer{Chats: []tg.ChatClass{&tg.Channel{ID: 1, Username: "channel", Broadcast: true}}}, a.resolveErr
}
func (a mediaMockAPI) MessagesGetHistory(context.Context, *tg.MessagesGetHistoryRequest) (tg.MessagesMessagesClass, error) {
	return &tg.MessagesMessages{Messages: a.messages}, a.historyErr
}
func mediaPhoto(id int, group int64, dc int) *tg.Message {
	return &tg.Message{ID: id, GroupedID: group, Media: &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: int64(id), DCID: dc, Sizes: []tg.PhotoSizeClass{&tg.PhotoSize{Type: "x", Size: 6}}}}}
}
func mediaOps(a mediaMockAPI, p *mediaMockPool) telegramCollectOps {
	return telegramCollectOps{
		prepare:    func(ctx context.Context) (context.Context, context.CancelFunc, error) { return ctx, func() {}, nil },
		authorized: func(context.Context) (bool, error) { return true, nil },
		api:        func() telegramCollectAPI { return a },
		photos: func() *telegramPhotoConnections {
			return newTelegramPhotoConnections(func(context.Context, int) (telegram.CloseInvoker, error) { return p, nil })
		},
	}
}
func TestMediaPoolsReusePrimaryAndFailures(t *testing.T) {
	primary, remote := &mediaMockPool{}, &mediaMockPool{}
	localCalls, remoteCalls := 0, map[int]int{}
	pools := telegramPhotoPools(2, func(int64) (telegram.CloseInvoker, error) { localCalls++; return primary, nil }, func(_ context.Context, dc int, _ int64) (telegram.CloseInvoker, error) {
		if dc == 2 {
			t.Fatal("primary DC authorization transfer")
		}
		remoteCalls[dc]++
		if dc == 4 {
			return nil, errors.New("secret")
		}
		return remote, nil
	})
	for _, dc := range []int{2, 2, 3, 3, 4, 4} {
		_, err := pools.api(context.Background(), dc)
		if (err != nil) != (dc == 4) {
			t.Fatal(err)
		}
	}
	pools.close()
	pools.close()
	if localCalls != 1 || remoteCalls[3] != 1 || remoteCalls[4] != 1 || primary.closed != 1 || remote.closed != 1 {
		t.Fatalf("%d %v %+v %+v", localCalls, remoteCalls, primary, remote)
	}
}
func TestMediaCollectPartialWarningsAndFlood(t *testing.T) {
	for _, flood := range []bool{false, true} {
		t.Run(fmt.Sprint(flood), func(t *testing.T) {
			failure := error(errors.New("secret"))
			if flood {
				failure = &tgerr.Error{Code: 420, Type: "FLOOD_WAIT", Argument: 60}
			}
			p := &mediaMockPool{fail: map[int]error{3: failure, 4: failure}}
			a := mediaMockAPI{messages: []tg.MessageClass{mediaPhoto(10, 0, 2), mediaPhoto(11, 8, 2), mediaPhoto(12, 8, 2), mediaPhoto(13, 0, 2), &tg.Message{ID: 14, Message: "text"}}}
			s := &telegramService{}
			memes, err := s.collect(context.Background(), "channel", mediaOps(a, p))
			if err == nil || strings.Contains(err.Error(), "secret") || p.closed != 1 {
				t.Fatalf("%v %+v", err, p)
			}
			want := 2
			if flood {
				want = 1
			}
			if len(memes) != want || len(memes[0].Images) != 1 {
				t.Fatalf("partial album or lost success: %+v", memes)
			}
			if flood {
				if p.calls != 3 || !s.floodUntil.After(time.Now()) {
					t.Fatal("did not stop/cool down")
				}
				if _, err := s.Collect(context.Background(), "channel"); err == nil || !strings.Contains(err.Error(), "冷却") {
					t.Fatal(err)
				}
			} else if p.calls != 4 || !strings.Contains(err.Error(), "2 次") {
				t.Fatal(err, p.calls)
			}
		})
	}
}
func TestMediaCollectStageFloodAndRedaction(t *testing.T) {
	for _, local := range []error{errTelegramPhotoTooLarge, errTelegramPhotoUnavailable, errTelegramPhotoContent, errTelegramPhotoRedirect} {
		s := &telegramService{}
		got := s.collectFailure("下载图片", fmt.Errorf("secret: %w", local)).Error()
		if strings.Contains(got, "secret") || !strings.Contains(got, local.Error()) {
			t.Fatal(got)
		}
	}
	for _, stage := range []string{"连接", "授权", "解析频道", "读取历史"} {
		t.Run(stage, func(t *testing.T) {
			s := &telegramService{}
			flood := &tgerr.Error{Code: 420, Type: "FLOOD_WAIT", Argument: 60, Message: "secret"}
			a := mediaMockAPI{}
			ops := mediaOps(a, &mediaMockPool{})
			switch stage {
			case "连接":
				ops.prepare = func(ctx context.Context) (context.Context, context.CancelFunc, error) { return ctx, func() {}, flood }
			case "授权":
				ops.authorized = func(context.Context) (bool, error) { return false, flood }
			case "解析频道":
				a.resolveErr = flood
				ops.api = func() telegramCollectAPI { return a }
			case "读取历史":
				a.historyErr = flood
				ops.api = func() telegramCollectAPI { return a }
			}
			_, err := s.collect(context.Background(), "channel", ops)
			if err == nil || !strings.Contains(err.Error(), stage) || strings.Contains(err.Error(), "secret") || !s.floodUntil.After(time.Now()) {
				t.Fatal(err)
			}
		})
	}
	s := &telegramService{}
	for _, err := range []error{errors.New("secret"), telegramDeliveryError("secret"), &tgerr.Error{Code: 400, Type: "secret", Message: "secret"}, &tgerr.Error{Code: 999, Type: "CHANNEL_PRIVATE", Message: "secret"}} {
		got := s.collectFailure("下载图片", fmt.Errorf("secret: %w", err)).Error()
		if strings.Contains(got, "secret") || strings.Contains(got, "RPC") {
			t.Fatal(got)
		}
	}
	got := s.collectFailure("下载图片", &tgerr.Error{Code: 400, Type: "FILE_REFERENCE_EXPIRED", Message: "secret"}).Error()
	if !strings.Contains(got, "RPC 400 FILE_REFERENCE_EXPIRED") || strings.Contains(got, "secret") {
		t.Fatal(got)
	}
}
