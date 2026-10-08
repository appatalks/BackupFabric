package ui

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed static/*
var content embed.FS

func Handler() http.Handler {
	root, err := fs.Sub(content, "static")
	if err != nil {
		panic(err)
	}
	return http.FileServer(http.FS(root))
}
