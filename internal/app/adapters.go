package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"golang.org/x/net/html"
)

type Post struct {
	Meme
	ImageURLs []string
}

func Endpoint(base, name string) string {
	base = strings.TrimRight(base, "/")
	for _, suffix := range []string{"/chat/completions", "/responses", "/models"} {
		if strings.HasSuffix(base, suffix) {
			base = strings.TrimSuffix(base, suffix)
			break
		}
	}
	return base + "/" + name
}

func FetchLimited(ctx context.Context, client *http.Client, url string, headers map[string]string, body []byte, max int64) ([]byte, string, error) {
	return fetchLimited(ctx, client, url, headers, body, max, 90*time.Second)
}

func fetchLimited(ctx context.Context, client *http.Client, url string, headers map[string]string, body []byte, max int64, timeout time.Duration) ([]byte, string, error) {
	if max < 0 {
		return nil, "", errors.New("响应大小限制不能为负数")
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	method := http.MethodGet
	if body != nil {
		method = http.MethodPost
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return nil, "", err
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer response.Body.Close()
	contentType := response.Header.Get("Content-Type")
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, contentType, &remoteHTTPError{response.StatusCode, response.Header.Get("Retry-After")}
	}
	if response.ContentLength > max {
		return nil, contentType, errors.New("远端响应超过大小限制")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, max))
	if err != nil {
		return nil, contentType, err
	}
	var extra [1]byte
	n, err := io.ReadFull(response.Body, extra[:])
	if n != 0 {
		return nil, contentType, errors.New("远端响应超过大小限制")
	}
	if err != nil && err != io.EOF {
		return nil, contentType, err
	}
	return data, contentType, nil
}

var channelNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_]{5,32}$`)
var photoURLPattern = regexp.MustCompile(`url[(]['"]?(https:[^'")]+)['"]?[)]`)

func htmlAttribute(node *html.Node, name string) string {
	for _, attr := range node.Attr {
		if attr.Key == name {
			return attr.Val
		}
	}
	return ""
}

func htmlClass(node *html.Node, name string) bool {
	if node.Type != html.ElementNode {
		return false
	}
	for _, class := range strings.Fields(htmlAttribute(node, "class")) {
		if class == name {
			return true
		}
	}
	return false
}

func walkHTML(node *html.Node, visit func(*html.Node)) {
	visit(node)
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		walkHTML(child, visit)
	}
}

func ParseChannel(source, channel string) ([]Post, error) {
	if !channelNamePattern.MatchString(channel) {
		return nil, errors.New("频道格式错误")
	}
	root, err := html.Parse(strings.NewReader(source))
	if err != nil {
		return nil, err
	}
	idPattern := regexp.MustCompile(`(?i)^` + regexp.QuoteMeta(channel) + `/[0-9]+$`)
	posts := make([]Post, 0)
	landingPage := false
	messagePage := false
	postIndexes := make(map[string]int)
	walkHTML(root, func(node *html.Node) {
		if htmlClass(node, "tgme_page_title") {
			landingPage = true
		}
		if !htmlClass(node, "tgme_widget_message") {
			return
		}
		messagePage = true
		id := htmlAttribute(node, "data-post")
		if !idPattern.MatchString(id) {
			return
		}
		var text strings.Builder
		var imageURLs []string
		date := ""
		foundTime := false
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walkHTML(child, func(desc *html.Node) {
				for parent := desc; parent != node; parent = parent.Parent {
					if htmlClass(parent, "tgme_widget_message") {
						return
					}
				}
				if htmlClass(desc, "tgme_widget_message_text") {
					walkHTML(desc, func(part *html.Node) {
						if part.Type == html.TextNode {
							text.WriteString(part.Data)
						}
						if part.Type == html.ElementNode && part.Data == "br" {
							text.WriteByte(10)
						}
					})
				}
				if htmlClass(desc, "tgme_widget_message_photo_wrap") {
					if match := photoURLPattern.FindStringSubmatch(htmlAttribute(desc, "style")); match != nil {
						if !slices.Contains(imageURLs, match[1]) {
							imageURLs = append(imageURLs, match[1])
						}
					}
				}
				if !foundTime && desc.Type == html.ElementNode && desc.Data == "time" {
					foundTime = true
					date = htmlAttribute(desc, "datetime")
				}
			})
		}
		value := strings.TrimSpace(text.String())
		// Count UTF-16 units like JavaScript, without splitting a Unicode character.
		units := 0
		for offset, char := range value {
			units++
			if char > 0xffff {
				units++
			}
			if units > 12000 {
				value = value[:offset]
				break
			}
		}
		created, err := time.Parse(time.RFC3339Nano, date)
		if err != nil || (value == "" && len(imageURLs) == 0) {
			return
		}
		key := strings.ToLower(id)
		if index, exists := postIndexes[key]; exists {
			post := &posts[index]
			for _, imageURL := range imageURLs {
				if !slices.Contains(post.ImageURLs, imageURL) {
					post.ImageURLs = append(post.ImageURLs, imageURL)
				}
			}
			if post.Text == "" {
				post.Text = value
			}
			return
		}
		postIndexes[key] = len(posts)
		posts = append(posts, Post{Meme: Meme{ID: id, Text: value, Channel: channel, URL: "https://t.me/" + id, CreatedAt: created.UTC().Format("2006-01-02T15:04:05.000Z")}, ImageURLs: imageURLs})
	})
	if len(posts) == 0 && landingPage && !messagePage {
		return nil, errors.New("Telegram 返回频道介绍页，未提供公开消息预览；当前匿名网页采集无法读取此频道")
	}
	return posts, nil
}

func CallAI(ctx context.Context, client *http.Client, ai AIConfig, instruction, text string, images []string) (string, error) {
	return callAI(ctx, client, ai, instruction, text, images, aiRequestTimeout, waitAIRetry)
}

func callAI(ctx context.Context, client *http.Client, ai AIConfig, instruction, text string, images []string, timeout time.Duration, wait func(context.Context, time.Duration) error) (string, error) {
	if ai.Model == "" {
		return "", errors.New("请先选择多模态模型")
	}
	if ai.Mode != "chat" && ai.Mode != "responses" {
		return "", errors.New("协议必须为 chat 或 responses")
	}
	textType, imageType, name := "input_text", "input_image", "responses"
	if ai.Mode == "chat" {
		textType, imageType, name = "text", "image_url", "chat/completions"
	}
	content := []map[string]any{{"type": textType, "text": text}}
	for i, original := range images {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		image, err := aiImagePreview(original)
		if err != nil {
			return "", fmt.Errorf("AI 图片 %d 预览失败：%w", i+1, err)
		}
		var value any = image
		if ai.Mode == "chat" {
			value = map[string]string{"url": image}
		}
		content = append(content, map[string]any{"type": imageType, "image_url": value})
	}
	request := map[string]any{"model": ai.Model}
	user := map[string]any{"role": "user", "content": content}
	if ai.Mode == "chat" {
		request["messages"] = []any{map[string]string{"role": "system", "content": instruction}, user}
	} else {
		request["instructions"] = instruction
		request["input"] = []any{user}
	}
	body, err := json.Marshal(request)
	if err != nil {
		return "", err
	}
	headers := map[string]string{"Content-Type": "application/json"}
	if ai.APIKey != "" {
		headers["Authorization"] = "Bearer " + ai.APIKey
	}
	data, attempts, err := fetchAI(ctx, client, Endpoint(ai.BaseURL, name), headers, body, timeout, wait)
	if err != nil {
		return "", err
	}
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		OutputText string `json:"output_text"`
		Output     []struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("模型响应解析失败（已尝试 %d 次）", attempts)
	}
	output := result.OutputText
	if ai.Mode == "chat" {
		output = ""
		if len(result.Choices) > 0 {
			output = result.Choices[0].Message.Content
		}
	} else if output == "" {
		var parts []string
		for _, item := range result.Output {
			for _, part := range item.Content {
				if part.Type == "output_text" {
					parts = append(parts, part.Text)
				}
			}
		}
		output = strings.Join(parts, string(rune(10)))
	}
	output = strings.TrimSpace(output)
	if output == "" {
		return "", fmt.Errorf("模型返回空内容（已尝试 %d 次）", attempts)
	}
	return output, nil
}

func ParseScore(output string) (float64, error) {
	output = strings.TrimSpace(output)
	if strings.HasPrefix(output, "```") {
		output = strings.TrimPrefix(output, "```")
		output = strings.TrimPrefix(output, "json")
		output = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(output), "```"))
	}
	var result struct {
		Score *float64 `json:"score"`
	}
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		return 0, fmt.Errorf("模型评分无效: %w", err)
	}
	if result.Score == nil || math.IsNaN(*result.Score) || math.IsInf(*result.Score, 0) || *result.Score < 0 || *result.Score > 100 {
		return 0, errors.New("模型评分无效")
	}
	return *result.Score, nil
}
