//go:build flutter_web_assets

package main

import (
	"embed"
	"io/fs"
)

//go:embed all:web_assets
var assets embed.FS

func webAssets() fs.FS {
	v, e := fs.Sub(assets, "web_assets")
	if e != nil {
		panic(e)
	}
	return v
}
