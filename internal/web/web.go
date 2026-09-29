// Package web embeds the console. It has no Node runtime or build dependency.
package web

import (
	"embed"
	"net/http"
)

//go:embed index.html app.js style.css
var files embed.FS

func Handler() http.Handler { return http.FileServerFS(files) }
