package telegram

import (
	"context"
	"errors"
	"fmt"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"sort"
	"time"
)

var (
	errTelegramPhotoTooLarge    = errors.New("图片超过大小限制")
	errTelegramPhotoUnavailable = errors.New("图片不可用或超过大小限制")
	errTelegramPhotoContent     = errors.New("图片内容无效")
	errTelegramPhotoRedirect    = errors.New("暂不支持图片 CDN 重定向")
)

type telegramCollectAPI interface {
	ContactsResolveUsername(context.Context, *tg.ContactsResolveUsernameRequest) (*tg.ContactsResolvedPeer, error)
	MessagesGetHistory(context.Context, *tg.MessagesGetHistoryRequest) (tg.MessagesMessagesClass, error)
}
type telegramCollectOps struct {
	prepare    func(context.Context) (context.Context, context.CancelFunc, error)
	authorized func(context.Context) (bool, error)
	api        func() telegramCollectAPI
	photos     func() *telegramPhotoConnections
}
type telegramPhotoConnection struct {
	pool telegram.CloseInvoker
	api  *tg.Client
	err  error
}
type telegramPhotoConnections struct {
	open        func(context.Context, int) (telegram.CloseInvoker, error)
	connections map[int]telegramPhotoConnection
}

func telegramPhotoPools(primary int, pool func(int64) (telegram.CloseInvoker, error), dcPool func(context.Context, int, int64) (telegram.CloseInvoker, error)) *telegramPhotoConnections {
	return newTelegramPhotoConnections(func(ctx context.Context, dc int) (telegram.CloseInvoker, error) {
		// Pool shares the primary session; DC performs authorization transfer.
		if dc == primary {
			return pool(1)
		}
		return dcPool(ctx, dc, 1)
	})
}

func newTelegramPhotoConnections(open func(context.Context, int) (telegram.CloseInvoker, error)) *telegramPhotoConnections {
	return &telegramPhotoConnections{open: open, connections: make(map[int]telegramPhotoConnection)}
}
func (p *telegramPhotoConnections) api(ctx context.Context, dc int) (*tg.Client, error) {
	if c, ok := p.connections[dc]; ok {
		return c.api, c.err
	}
	pool, err := p.open(ctx, dc)
	c := telegramPhotoConnection{pool: pool, err: err}
	if err == nil {
		c.api = tg.NewClient(pool)
	}
	p.connections[dc] = c
	return c.api, c.err
}
func (p *telegramPhotoConnections) close() {
	for dc, c := range p.connections {
		if c.pool != nil {
			_ = c.pool.Close()
		}
		delete(p.connections, dc)
	}
}
func (s *telegramService) collectFailure(stage string, err error) error {
	if delay, ok := tgerr.AsFloodWait(err); ok {
		until := time.Now().Add(delay)
		if until.After(s.floodUntil) {
			s.floodUntil = until
		}
		return fmt.Errorf("Telegram %s失败：请求过于频繁，请等待 %d 秒", stage, int(delay.Seconds()))
	}
	detail := "请检查配置、登录信息或网络后重试"
	for _, local := range []error{errTelegramPhotoTooLarge, errTelegramPhotoUnavailable, errTelegramPhotoContent, errTelegramPhotoRedirect} {
		if errors.Is(err, local) {
			return fmt.Errorf("Telegram %s失败：%s", stage, local.Error())
		}
	}
	if errors.Is(err, context.Canceled) {
		detail = "操作已取消"
	} else if errors.Is(err, context.DeadlineExceeded) {
		detail = "操作超时"
	} else if rpc, ok := tgerr.As(err); ok {
		// Never expose RPC messages, arguments, or unrecognized server-supplied types.
		allowed := map[string]int{
			"AUTH_BYTES_INVALID": 400, "FILE_ID_INVALID": 400,
			"AUTH_KEY_UNREGISTERED": 401, "SESSION_REVOKED": 401, "SESSION_EXPIRED": 401,
			"USERNAME_INVALID": 400, "USERNAME_NOT_OCCUPIED": 400, "CHANNEL_PRIVATE": 400,
			"CHANNEL_INVALID": 400, "PEER_ID_INVALID": 400, "FILE_REFERENCE_EXPIRED": 400,
			"FILE_REFERENCE_INVALID": 400, "LOCATION_INVALID": 400, "DC_ID_INVALID": 400,
		}
		if code, ok := allowed[rpc.Type]; ok && code == rpc.Code {
			detail = fmt.Sprintf("RPC %d %s", code, rpc.Type)
		}
	}
	return fmt.Errorf("Telegram %s失败：%s", stage, detail)
}

type telegramCollectWarnings map[string]int

func (w telegramCollectWarnings) add(err error) { w[err.Error()]++ }
func (w telegramCollectWarnings) err() error {
	keys := make([]string, 0, len(w))
	for key := range w {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var result []error
	for _, key := range keys {
		result = append(result, fmt.Errorf("%s（%d 次）", key, w[key]))
	}
	return errors.Join(result...)
}
