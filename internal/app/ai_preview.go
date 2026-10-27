package app

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	"image/draw"
	"image/jpeg"
	_ "image/png"
	"strings"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const (
	aiPreviewLongEdge  = 1600
	aiPreviewMaxBytes  = 20 * 1024 * 1024
	aiPreviewMaxPixels = 25_000_000
)

func aiImagePreview(original string) (string, error) {
	if len(original) > base64.StdEncoding.EncodedLen(aiPreviewMaxBytes)+32 {
		return "", errors.New("图片输入超过 20 MiB 限制")
	}
	header, encoded, ok := strings.Cut(original, ",")
	if !ok || (header != "data:image/jpeg;base64" && header != "data:image/png;base64" && header != "data:image/webp;base64") {
		return "", errors.New("需要 JPEG、PNG 或 WebP 的 base64 图片")
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", errors.New("图片 base64 无效")
	}
	if len(data) > aiPreviewMaxBytes {
		return "", errors.New("图片输入超过 20 MiB 限制")
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return "", errors.New("无法读取图片尺寸或格式")
	}
	if header != "data:image/"+format+";base64" {
		return "", errors.New("图片格式与 MIME 不一致")
	}
	if config.Width <= 0 || config.Height <= 0 || config.Width > aiPreviewMaxPixels/config.Height {
		return "", errors.New("图片解码超过 2500 万像素限制")
	}
	source, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return "", errors.New("图片解码失败")
	}
	w, h := config.Width, config.Height
	if longest := max(w, h); longest > aiPreviewLongEdge {
		w = max(1, w*aiPreviewLongEdge/longest)
		h = max(1, h*aiPreviewLongEdge/longest)
	}
	preview := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(preview, preview.Bounds(), image.White, image.Point{}, draw.Src)
	xdraw.ApproxBiLinear.Scale(preview, preview.Bounds(), source, source.Bounds(), draw.Over, nil)
	var output bytes.Buffer
	if err := jpeg.Encode(&output, preview, &jpeg.Options{Quality: 85}); err != nil {
		return "", errors.New("JPEG 预览编码失败")
	}
	// Keeping a smaller original can also keep dimensions above the preview target.
	if output.Len() >= len(data) {
		return original, nil
	}
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(output.Bytes()), nil
}
