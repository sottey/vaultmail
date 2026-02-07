package web

import (
	"embed"
	"html/template"
	"strings"
)

//go:embed templates/*.html
var templateFS embed.FS

type Server struct {
	Index   *template.Template
	Message *template.Template
	Login   *template.Template
}

func NewServer() (*Server, error) {
	funcs := template.FuncMap{
		"inc": func(v int) int { return v + 1 },
		"dec": func(v int) int {
			if v <= 1 {
				return 1
			}
			return v - 1
		},
		"hasPrefix": strings.HasPrefix,
	}
	index, err := template.New("base").Funcs(funcs).ParseFS(templateFS, "templates/base.html", "templates/index.html")
	if err != nil {
		return nil, err
	}
	message, err := template.New("base").Funcs(funcs).ParseFS(templateFS, "templates/base.html", "templates/message.html")
	if err != nil {
		return nil, err
	}
	login, err := template.New("base").Funcs(funcs).ParseFS(templateFS, "templates/base.html", "templates/login.html")
	if err != nil {
		return nil, err
	}

	return &Server{Index: index, Message: message, Login: login}, nil
}
