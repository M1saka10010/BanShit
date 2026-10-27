package public

import "embed"

//go:embed *.html *.svg *.js css js
var Assets embed.FS
