package app

import (
	_ "embed"
	"net/http"
)

//go:embed adminui/index.html
var adminUIHTML []byte

func (s *Server) serveAdminUI(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/admin-ui" && r.URL.Path != "/admin-ui/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(adminUIHTML)
}
