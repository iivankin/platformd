package ui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var assets embed.FS

func Files() fs.FS {
	sub, err := fs.Sub(assets, "dist")
	if err != nil {
		panic("embedded UI assets are missing: " + err.Error())
	}
	return sub
}
