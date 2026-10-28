package telegram

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
)

const telegramPhotoLimit = 4 << 20
const telegramAlbumLimit = 20 << 20

type telegramLimitedWriter struct {
	bytes.Buffer
	limit int
}

func (w *telegramLimitedWriter) Write(p []byte) (int, error) {
	if len(p) > w.limit-w.Len() {
		return 0, errTelegramPhotoTooLarge
	}
	return w.Buffer.Write(p)
}

func (s *telegramService) Collect(ctx context.Context, channel string) ([]Post, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.collect(ctx, channel, telegramCollectOps{
		prepare: s.operation,
		authorized: func(ctx context.Context) (bool, error) {
			st, err := s.client.Auth().Status(ctx)
			return err == nil && st.Authorized, err
		},
		api: func() telegramCollectAPI { return s.client.API() },
		photos: func() *telegramPhotoConnections {
			return telegramPhotoPools(s.client.Config().ThisDC, s.client.Pool, s.client.DC)
		},
	})
}

func (s *telegramService) collect(ctx context.Context, channel string, ops telegramCollectOps) ([]Post, error) {
	if time.Now().Before(s.floodUntil) {
		return nil, fmt.Errorf("Telegram 采集冷却中，请等待 %d 秒", int(time.Until(s.floodUntil).Seconds())+1)
	}
	channel = strings.TrimPrefix(channel, "@")
	if !regexp.MustCompile("^[a-zA-Z][a-zA-Z0-9_]{3,31}$").MatchString(channel) {
		return nil, errors.New("Telegram 公开频道用户名无效")
	}
	ctx, cancel, err := ops.prepare(ctx)
	defer cancel()
	if err != nil {
		return nil, s.collectFailure("连接", err)
	}
	authorized, err := ops.authorized(ctx)
	if err != nil {
		return nil, s.collectFailure("授权", err)
	}
	if !authorized {
		return nil, errors.New("请先登录 Telegram")
	}
	s.state = "authorized"
	resolved, err := ops.api().ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{Username: channel})
	if err != nil {
		return nil, s.collectFailure("解析频道", err)
	}
	var peer *tg.InputPeerChannel
	for _, chat := range resolved.Chats {
		if c, ok := chat.(*tg.Channel); ok && c.Broadcast && telegramChannelUsernameMatches(c, channel) {
			peer = &tg.InputPeerChannel{ChannelID: c.ID, AccessHash: c.AccessHash}
			if c.Username != "" {
				channel = c.Username
			}
			channel = strings.ToLower(channel)
			break
		}
	}
	if peer == nil {
		return nil, errors.New("仅支持公开 Telegram 频道")
	}
	history, err := ops.api().MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{Peer: peer, Limit: 30})
	if err != nil {
		return nil, s.collectFailure("读取历史", err)
	}
	var messages []tg.MessageClass
	moreHistory := false
	switch h := history.(type) {
	case *tg.MessagesChannelMessages:
		messages = h.Messages
		moreHistory = h.Count > len(messages)
	case *tg.MessagesMessages:
		messages = h.Messages
	case *tg.MessagesMessagesSlice:
		messages = h.Messages
		moreHistory = h.Count > len(messages)
	default:
		return nil, errors.New("Telegram 历史消息不可用")
	}
	// The oldest group may continue on the next page; omit it rather than publish a partial album.
	groups := telegramMessageGroups(messages, moreHistory)
	result := make([]Post, 0, len(groups))
	photos := ops.photos()
	defer photos.close()
	warnings := telegramCollectWarnings{}
	for _, group := range groups {
		if err := ctx.Err(); err != nil {
			warnings.add(s.collectFailure("下载图片", err))
			break
		}
		meme, ok, err := telegramConvertGroup(ctx, channel, group, photos.downloadPhoto)
		if err != nil {
			warnings.add(s.collectFailure("下载图片", err))
			if _, flood := tgerr.AsFloodWait(err); flood || ctx.Err() != nil {
				break
			}
			continue
		}
		if ok {
			result = append(result, meme)
		}
	}
	return result, warnings.err()
}

// Collectible public usernames can be present only in Usernames.
func telegramChannelUsernameMatches(c *tg.Channel, username string) bool {
	if strings.EqualFold(c.Username, username) {
		return true
	}
	for _, alias := range c.Usernames {
		if alias.Active && strings.EqualFold(alias.Username, username) {
			return true
		}
	}
	return false
}

func telegramMessageGroups(messages []tg.MessageClass, omitBoundary bool) [][]*tg.Message {
	var boundary int64
	if omitBoundary && len(messages) > 0 {
		if m, ok := messages[len(messages)-1].(*tg.Message); ok {
			boundary = m.GroupedID
		}
	}
	groups := make([][]*tg.Message, 0)
	indexes := map[int64]int{}
	for _, raw := range messages {
		m, ok := raw.(*tg.Message)
		if !ok {
			continue
		}
		if boundary != 0 && m.GroupedID == boundary {
			continue
		}
		if m.GroupedID == 0 {
			groups = append(groups, []*tg.Message{m})
			continue
		}
		i, found := indexes[m.GroupedID]
		if !found {
			i = len(groups)
			indexes[m.GroupedID] = i
			groups = append(groups, nil)
		}
		groups[i] = append(groups[i], m)
	}
	return groups
}

type telegramPhotoLoader func(context.Context, *tg.Photo) ([]byte, error)

func telegramConvertGroup(ctx context.Context, channel string, group []*tg.Message, load telegramPhotoLoader) (Post, bool, error) {
	if len(group) == 0 || len(group) > 10 {
		return Post{}, false, nil
	}
	channel = strings.ToLower(channel)
	for _, m := range group {
		if m.GroupedID != 0 && time.Since(time.Unix(int64(m.Date), 0)) < 10*time.Second {
			return Post{}, false, nil
		}
	}
	group = append([]*tg.Message(nil), group...)
	sort.Slice(group, func(i, j int) bool { return group[i].ID < group[j].ID })
	m := Post{ID: fmt.Sprintf("%s/%d", channel, group[0].ID), Channel: channel, CreatedAt: time.Unix(int64(group[0].Date), 0).UTC().Format("2006-01-02T15:04:05.000Z"), Images: []string{}}
	m.URL = "https://t.me/" + m.ID
	var photos []*tg.Photo
	var text []string
	for _, message := range group {
		if message.Message != "" {
			text = append(text, message.Message)
		}
		switch media := message.Media.(type) {
		case nil, *tg.MessageMediaEmpty:
		case *tg.MessageMediaPhoto:
			photo, ok := media.Photo.(*tg.Photo)
			if !ok {
				return Post{}, false, nil
			}
			photos = append(photos, photo)
		default:
			return Post{}, false, nil
		}
	}
	total := 0
	for _, photo := range photos {
		data, err := load(ctx, photo)
		if err != nil {
			return Post{}, false, err
		}
		if len(data) == 0 || len(data) > telegramPhotoLimit {
			return Post{}, false, nil
		}
		total += len(data)
		if total > telegramAlbumLimit {
			return Post{}, false, nil
		}
		mime := http.DetectContentType(data)
		if !strings.HasPrefix(mime, "image/") {
			return Post{}, false, errTelegramPhotoContent
		}
		m.Images = append(m.Images, "data:"+mime+";base64,"+base64.StdEncoding.EncodeToString(data))
	}
	m.Text = strings.Join(text, string(rune(10)))
	return m, m.Text != "" || len(m.Images) > 0, nil
}

func (s *telegramPhotoConnections) downloadPhoto(ctx context.Context, p *tg.Photo) ([]byte, error) {
	thumb := ""
	size := 0
	for _, raw := range p.Sizes {
		switch v := raw.(type) {
		case *tg.PhotoSize:
			if v.Size > size {
				size = v.Size
				thumb = v.Type
			}
		case *tg.PhotoSizeProgressive:
			for _, n := range v.Sizes {
				if n > size {
					size = n
					thumb = v.Type
				}
			}
		}
	}
	if thumb == "" || size > telegramPhotoLimit {
		return nil, errTelegramPhotoUnavailable
	}
	api, err := s.api(ctx, p.DCID)
	if err != nil {
		return nil, err
	}
	w := &telegramLimitedWriter{limit: telegramPhotoLimit}
	location := &tg.InputPhotoFileLocation{ID: p.ID, AccessHash: p.AccessHash, FileReference: p.FileReference, ThumbSize: thumb}
	const chunkSize = 128 << 10
	for offset := 0; offset <= telegramPhotoLimit; offset += chunkSize {
		result, err := api.UploadGetFile(ctx, &tg.UploadGetFileRequest{Location: location, Offset: int64(offset), Limit: chunkSize})
		if err != nil {
			return nil, err
		}
		file, ok := result.(*tg.UploadFile)
		if !ok {
			return nil, errTelegramPhotoRedirect
		}
		if _, err = w.Write(file.Bytes); err != nil {
			return nil, err
		}
		if len(file.Bytes) < chunkSize {
			return w.Bytes(), nil
		}
	}
	return nil, errTelegramPhotoTooLarge
}
