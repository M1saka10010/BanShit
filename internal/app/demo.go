package app

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// runDemo uses isolated temporary storage and never starts the collector.
func runDemo() error {
	dir, err := os.MkdirTemp("", "banshit-demo-")
	if err != nil {
		return err
	}
	password := token(8)
	c := Config{Host: "127.0.0.1", Port: 3000, AdminPassword: password, Channels: []string{"demo_memes"}, IntervalMinutes: 10, OptimizeEveryHours: 24, InterestPrompt: "【演示】偏好程序员日常、打工人的自嘲、猫猫和出其不意的反差幽默。", AI: AIConfig{BaseURL: "https://api.openai.com/v1", Mode: "chat"}}
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "config.json")
	if err = os.WriteFile(path, b, 0600); err != nil {
		return err
	}
	a, err := NewApp(path, dir)
	if err != nil {
		return err
	}
	defer a.Close()
	captions := []string{"周五下班前：只改一行代码。周六凌晨：怎么还没修好。", "我：这次一定早点睡。也是我：再看一个 meme。", "产品：这个需求很简单。开发：我的表情逐渐消失。", "本地运行：完美。线上运行：你谁？", "老板问进度时，我的 CPU 使用率突然达到 100%。", "今天的运动量：在不同的浏览器标签之间反复横跳。", "猫：这个键盘挺暖和。代码：;;;;;;;;;;;;;;;;", "我的存款和我的头发，总有一个先离开我。", "写代码两小时，给变量起名字三小时。", "周一的我：电量 1%，正在搜索充电器。", "没有解决问题，但是成功把报错换了一种颜色。", "当你终于修好了 bug，却不知道自己做了什么。", "别人下班：生活开始。我下班：系统正在关机。", "咖啡不是饮料，是我的运行时依赖。", "刚说完绝对不会出问题，监控就响了。", "需求文档：一页。群聊补充：999+。"}
	for i, text := range captions {
		img := image.NewRGBA(image.Rect(0, 0, 480, 300))
		bg := color.RGBA{uint8(45 + i*7), uint8(38 + i*5), uint8(72 + i*8), 255}
		for y := 0; y < 300; y++ {
			for x := 0; x < 480; x++ {
				p := bg
				dx, dy := x-240, y-140
				if dx*dx+dy*dy < 95*95 {
					p = color.RGBA{245, 210, uint8(80 + i*7), 255}
				}
				if ((x-207)*(x-207)+(y-122)*(y-122) < 100) || ((x-273)*(x-273)+(y-122)*(y-122) < 100) {
					p = color.RGBA{30, 30, 40, 255}
				}
				if y > 166 && y < 175 && x > 205 && x < 275 {
					p = color.RGBA{30, 30, 40, 255}
				}
				img.SetRGBA(x, y, p)
			}
		}
		var buf bytes.Buffer
		if err = png.Encode(&buf, img); err != nil {
			return err
		}
		data := "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
		images := []string{data}
		if i%3 == 0 {
			images = []string{}
		}
		if i%3 == 2 {
			images = []string{data, data, data}
		}
		err = a.storeMeme(Meme{ID: fmt.Sprintf("demo_memes/%d", i+1), Text: "【演示样例】" + text, Images: images, Channel: "demo_memes", Score: float64(99 - i), CreatedAt: time.Now().Add(-time.Duration(i) * time.Minute).UTC().Format(time.RFC3339)})
		if err != nil {
			return err
		}
	}
	pool, err := a.rankingPool(c)
	if err != nil {
		return err
	}
	if err = a.publishFeed(c, pool); err != nil {
		return err
	}
	a.status.LastRun = time.Now().UTC().Format(time.RFC3339)
	a.status.LastError = "演示模式：采集、模型请求与提示词自动优化已禁用。示例为本地生成，并非 Telegram 内容。"
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/admin/reevaluate" || strings.HasPrefix(r.URL.Path, "/api/admin/telegram") || r.URL.Path == "/api/admin/models" || r.URL.Path == "/api/admin/refresh" || r.URL.Path == "/api/admin/optimize" {
			fail(w, 403, "演示模式不调用外部服务，请使用正式模式体验此功能")
			return
		}
		a.ServeHTTP(w, r)
	})
	log.Printf("DEMO http://127.0.0.1:3000 | admin password: %s | isolated data: %s", password, dir)
	return (&http.Server{Addr: "127.0.0.1:3000", Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}).ListenAndServe()
}
