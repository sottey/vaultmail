package web

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed media/*
var mediaFS embed.FS

func mediaFileServer() (http.Handler, error) {
	sub, err := fs.Sub(mediaFS, "media")
	if err != nil {
		return nil, err
	}
	return http.FileServer(http.FS(sub)), nil
}
