package app

import (
	"testing"
	"time"
)

func TestTelegramSchedulerDefersStartupCollection(t *testing.T) {
	a := testApp(t)
	a.mu.Lock()
	a.config.Telegram.Enabled = true
	a.mu.Unlock()
	before := time.Now()
	a.StartScheduler()
	a.mu.Lock()
	last := a.lastRefresh
	running := a.status.Running
	a.mu.Unlock()
	a.cancel()
	a.wg.Wait()
	if last.Before(before) || running {
		t.Fatal("account collection must wait for its interval after startup")
	}
}
