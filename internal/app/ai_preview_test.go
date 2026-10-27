package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

func previewFixture(t *testing.T, w, h int, seed byte, compressed bool) string {
	t.Helper()
	m := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			m.SetNRGBA(x, y, color.NRGBA{byte(x) + seed, byte(y), byte(x * y), 255})
		}
	}
	var b bytes.Buffer
	if compressed {
		if err := jpeg.Encode(&b, m, &jpeg.Options{Quality: 100}); err != nil {
			t.Fatal(err)
		}
		return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(b.Bytes())
	}
	encoder := png.Encoder{CompressionLevel: png.NoCompression}
	if err := encoder.Encode(&b, m); err != nil {
		t.Fatal(err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(b.Bytes())
}

func previewDecode(t *testing.T, s string) image.Image {
	t.Helper()
	_, v, _ := strings.Cut(s, ",")
	b, e := base64.StdEncoding.DecodeString(v)
	if e != nil {
		t.Fatal(e)
	}
	m, _, e := image.Decode(bytes.NewReader(b))
	if e != nil {
		t.Fatal(e)
	}
	return m
}

func TestAIPreviewDimensions(t *testing.T) {
	for _, tc := range []struct{ w, h, ow, oh int }{{2400, 1200, 1600, 800}, {1200, 2400, 800, 1600}, {1700, 1700, 1600, 1600}, {80, 40, 80, 40}, {3200, 1, 1600, 1}} {
		for _, jpegInput := range []bool{false, true} {
			original := previewFixture(t, tc.w, tc.h, 1, jpegInput)
			got, e := aiImagePreview(original)
			if e != nil {
				t.Fatal(e)
			}
			if got == original {
				t.Fatal("fixture did not compress")
			}
			m := previewDecode(t, got)
			if m.Bounds().Dx() != tc.ow || m.Bounds().Dy() != tc.oh {
				t.Fatal(m.Bounds())
			}
			if len(got) >= len(original) {
				t.Fatal("not smaller")
			}
		}
	}
}

func TestAIPreviewAlphaAndFallback(t *testing.T) {
	m := image.NewNRGBA(image.Rect(0, 0, 128, 64))
	for y := 0; y < 64; y++ {
		for x := 64; x < 128; x++ {
			m.SetNRGBA(x, y, color.NRGBA{255, 0, 0, 128})
		}
	}
	var b bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.NoCompression}
	if e := enc.Encode(&b, m); e != nil {
		t.Fatal(e)
	}
	got, e := aiImagePreview("data:image/png;base64," + base64.StdEncoding.EncodeToString(b.Bytes()))
	if e != nil {
		t.Fatal(e)
	}
	out := previewDecode(t, got)
	for _, p := range []struct {
		x       int
		r, g, b uint8
	}{{20, 255, 255, 255}, {100, 255, 127, 127}} {
		c := color.NRGBAModel.Convert(out.At(p.x, 32)).(color.NRGBA)
		if absPreview(int(c.R)-int(p.r)) > 3 || absPreview(int(c.G)-int(p.g)) > 3 || absPreview(int(c.B)-int(p.b)) > 3 {
			t.Fatal(c)
		}
	}
	for _, w := range []int{1, 2000} {
		b.Reset()
		if e := png.Encode(&b, image.NewGray(image.Rect(0, 0, w, 1))); e != nil {
			t.Fatal(e)
		}
		original := "data:image/png;base64," + base64.StdEncoding.EncodeToString(b.Bytes())
		got, e = aiImagePreview(original)
		if e != nil || got != original {
			t.Fatalf("fallback %d: %v", w, e)
		}
	}
}
func absPreview(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func TestAIPreviewRejectsInvalid(t *testing.T) {
	valid := previewFixture(t, 1, 1, 0, false)
	_, s, _ := strings.Cut(valid, ",")
	b, _ := base64.StdEncoding.DecodeString(s)
	binary.BigEndian.PutUint32(b[16:20], 50000)
	binary.BigEndian.PutUint32(b[20:24], 50000)
	binary.BigEndian.PutUint32(b[29:33], crc32.ChecksumIEEE(b[12:29]))
	for _, bad := range []string{"", "data:image/png;base64,?", "data:image/png;base64,aA==", strings.Replace(valid, "image/png", "image/jpeg", 1), "data:image/png;base64," + base64.StdEncoding.EncodeToString(b), strings.Repeat("x", base64.StdEncoding.EncodedLen(aiPreviewMaxBytes)+33), valid[:len(valid)-16]} {
		if _, e := aiImagePreview(bad); e == nil {
			t.Fatal("invalid image accepted")
		}
		calls := 0
		client := &http.Client{Transport: aiRoundTripFunc(func(*http.Request) (*http.Response, error) { calls++; return nil, nil })}
		_, e := CallAI(context.Background(), client, AIConfig{Model: "m", Mode: "chat"}, "", "", []string{valid, bad})
		if e == nil || !strings.Contains(e.Error(), "图片 2") || calls != 0 {
			t.Fatalf("%v calls=%d", e, calls)
		}
	}
}

func TestAIPreviewWebP(t *testing.T) {
	original := "data:image/webp;base64,UklGRiIAAABXRUJQVlA4IBYAAAAwAQCdASoBAAEADsD+JaQAA3AAAAAA"
	got, e := aiImagePreview(original)
	if e != nil {
		t.Fatal(e)
	}
	if previewDecode(t, got).Bounds().Dx() != 1 {
		t.Fatal("webp dimensions")
	}
}

func TestAIPreviewProtocolsRetryAndIsolation(t *testing.T) {
	for _, mode := range []string{"chat", "responses"} {
		originals := []string{previewFixture(t, 1800, 100, 5, false), previewFixture(t, 90, 90, 50, false), previewFixture(t, 90, 90, 50, false)}
		saved := append([]string(nil), originals...)
		want := make([]string, len(originals))
		for i, s := range originals {
			var e error
			want[i], e = aiImagePreview(s)
			if e != nil {
				t.Fatal(e)
			}
		}
		calls := 0
		var first []byte
		client := &http.Client{Transport: aiRoundTripFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			body, _ := io.ReadAll(r.Body)
			if calls == 1 {
				first = body
			} else if !bytes.Equal(first, body) {
				t.Error("retry regenerated body")
			}
			var request map[string]any
			if e := json.Unmarshal(body, &request); e != nil {
				t.Fatal(e)
			}
			key, index := "input", 0
			if mode == "chat" {
				key, index = "messages", 1
			}
			content := request[key].([]any)[index].(map[string]any)["content"].([]any)
			got := []string{}
			for _, part := range content[1:] {
				v := part.(map[string]any)["image_url"]
				if mode == "chat" {
					v = v.(map[string]any)["url"]
				}
				got = append(got, v.(string))
			}
			if !reflect.DeepEqual(got, want) {
				t.Error("image order/content changed")
			}
			status := 503
			if calls == 2 {
				status = 200
			}
			return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"output_text":"ok","choices":[{"message":{"content":"ok"}}]}`))}, nil
		})}
		_, e := callAI(context.Background(), client, AIConfig{Model: "m", Mode: mode, BaseURL: "http://example.test"}, "", "", originals, aiRequestTimeout, func(context.Context, time.Duration) error {
			if !reflect.DeepEqual(originals, saved) {
				t.Error("caller slice mutated")
			}
			// A retry must use the serialized body, not read or recompress input again.
			originals[0] = "invalid after first request"
			return nil
		})
		if e != nil || calls != 2 {
			t.Fatalf("%v %d", e, calls)
		}
	}
}
