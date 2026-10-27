package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const aiRequestTimeout = 300 * time.Second
const maxAIRetryAfter = 300 * time.Second

type remoteHTTPError struct {
	status     int
	retryAfter string
}

func (e *remoteHTTPError) Error() string { return fmt.Sprintf("远端服务 HTTP %d", e.status) }

func waitAIRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ctx.Err()
	}
}

func aiRetryAfter(value string, now time.Time) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	if strings.Trim(value, "0123456789") == "" {
		seconds, err := strconv.ParseUint(value, 10, 64)
		if err != nil || seconds > uint64(maxAIRetryAfter/time.Second) {
			return 0, true
		}
		return time.Duration(seconds) * time.Second, false
	}
	if date, err := http.ParseTime(value); err == nil {
		delay := date.Sub(now)
		if delay > maxAIRetryAfter {
			return 0, true
		}
		if delay > 0 {
			return delay, false
		}
	}
	return 0, false
}

func fetchAI(ctx context.Context, client *http.Client, url string, headers map[string]string, body []byte, timeout time.Duration, wait func(context.Context, time.Duration) error) ([]byte, int, error) {
	if client == nil {
		client = http.DefaultClient
	}
	aiClient := *client
	aiClient.Timeout = timeout
	attempts := 0
	failure := func(message string) ([]byte, int, error) {
		return nil, attempts, fmt.Errorf("%s（已尝试 %d 次）", message, attempts)
	}
	for {
		if ctx.Err() != nil {
			return failure("模型请求已取消或总期限已到")
		}
		attempts++
		data, _, err := fetchLimited(ctx, &aiClient, url, headers, body, 8*1024*1024, timeout)
		if ctx.Err() != nil {
			return failure("模型请求已取消或总期限已到")
		}
		if err == nil {
			return data, attempts, nil
		}
		message := "模型请求失败（网络、请求或响应读取异常）"
		retry := false
		delay := time.Second << (attempts - 1)
		var status *remoteHTTPError
		var network net.Error
		if errors.As(err, &status) {
			message = status.Error()
			retry = status.status == 429 || status.status == 502 || status.status == 503 || status.status == 504
			if retry {
				after, tooLong := aiRetryAfter(status.retryAfter, time.Now())
				if tooLong {
					return failure(message + "；Retry-After 超过 300 秒，已停止自动重试以避免提前请求，请稍后手动重试")
				}
				if after > delay {
					delay = after
				}
			}
		} else if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &network) && (network.Timeout() || network.Temporary())) {
			retry = true
			message = "模型请求超时或临时网络错误"
		}
		if !retry || attempts == 4 {
			return failure(message)
		}
		if err := wait(ctx, delay); err != nil {
			return failure("模型请求已取消或总期限已到")
		}
	}
}
