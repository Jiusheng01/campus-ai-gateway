package app

import (
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

// adminUIFS contains the production Vite build. The frontend source lives in
// web/admin; dev.ps1 and CI rebuild this directory before Go tests/builds.
//
//go:embed adminui/dist
var adminUIFS embed.FS

func (s *Server) serveAdminUI(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/admin-ui" && !strings.HasPrefix(r.URL.Path, "/admin-ui/") {
		http.NotFound(w, r)
		return
	}

	root, err := fs.Sub(adminUIFS, "adminui/dist")
	if err != nil {
		http.Error(w, "admin ui unavailable", http.StatusInternalServerError)
		return
	}

	name := strings.TrimPrefix(r.URL.Path, "/admin-ui/")
	if r.URL.Path == "/admin-ui" || name == "" {
		name = "index.html"
	}
	name = path.Clean(strings.TrimPrefix(name, "/"))
	if name == "." || strings.HasPrefix(name, "../") {
		http.NotFound(w, r)
		return
	}

	data, err := fs.ReadFile(root, name)
	if err != nil {
		// The UI is a client-side workspace. Unknown non-asset paths fall back
		// to index.html so future frontend routes can be added without Go changes.
		if strings.Contains(path.Base(name), ".") {
			http.NotFound(w, r)
			return
		}
		data, err = fs.ReadFile(root, "index.html")
		if err != nil {
			http.Error(w, "admin ui unavailable", http.StatusInternalServerError)
			return
		}
		name = "index.html"
	}

	contentType := mime.TypeByExtension(path.Ext(name))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if name == "index.html" {
		contentType = "text/html; charset=utf-8"
		w.Header().Set("Cache-Control", "no-cache")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	w.Header().Set("Content-Type", contentType)
	_, _ = w.Write(data)
}
