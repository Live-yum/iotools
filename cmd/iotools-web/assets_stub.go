//go:build !flutter_web_assets

package main

import "io/fs"

// Builds without Flutter assets deliberately refuse startup; no dummy UI ships.
func webAssets() fs.FS { return nil }
