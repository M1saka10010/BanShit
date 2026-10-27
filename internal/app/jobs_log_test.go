package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"testing"
)

func TestCollectionLogs(t *testing.T) {
	var output bytes.Buffer
	old := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(old)
	logCollectionChannel("网页", "channel", 3, 2, errors.New("secret-token\nforged"))
	text := output.String()
	if !strings.Contains(text, "candidates=3 added=2") || strings.Contains(text, "secret") || strings.Contains(text, "forged") {
		t.Fatal(text)
	}
	for _, tc := range []struct {
		err  error
		want string
	}{
		{context.Canceled, "canceled"},
		{context.DeadlineExceeded, "timeout"},
		{fmt.Errorf("secret: %w", &remoteHTTPError{status: 502}), "http_502"},
	} {
		if got := collectionLogError(tc.err); got != tc.want {
			t.Fatal(got)
		}
	}
	a := testApp(t)
	a.mu.Lock()
	a.config.AI.Model = ""
	a.mu.Unlock()
	if !a.launch(false) {
		t.Fatal("launch rejected")
	}
	a.wg.Wait()
	text = output.String()
	if !strings.Contains(text, "任务开始 type=采集") || !strings.Contains(text, "任务结束 type=采集") {
		t.Fatal(text)
	}
}
