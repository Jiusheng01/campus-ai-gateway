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

// adminUIContentSecurityPolicy is the CSP applied to every Admin UI response.
//
// The console holds access tokens in memory and issues privileged control-plane
// calls, so it must be able to load only its own bundle and talk only to its own
// origin. The directives:
//
//   - default-src 'self'  everything not listed below comes from this origin;
//   - base-uri 'none'     a <base> tag cannot re-point relative URLs elsewhere;
//   - object-src 'none'   no plugins, no embedded documents;
//   - frame-ancestors 'none'  this UI may not be framed at all;
//   - form-action 'self'  forms may only post back to this origin;
//   - script-src 'self'   no inline or eval'd script — the bundle is a file;
//   - connect-src 'self'  API calls go to the same origin;
//   - img-src 'self' data:  inlined favicon/data images keep working;
//   - font-src 'self';
//   - style-src 'self' 'unsafe-inline'  the React components set inline style
//     attributes; Phase 1 tolerates that one exception, and only that one.
//     script-src keeps 'unsafe-inline' and 'unsafe-eval' forbidden.
//
// HSTS is deliberately absent: it belongs to the TLS entry point, not to an
// application that also serves plain HTTP in development.
const adminUIContentSecurityPolicy = "default-src 'self'; base-uri 'none'; object-src 'none'; " +
	"frame-ancestors 'none'; form-action 'self'; script-src 'self'; connect-src 'self'; " +
	"img-src 'self' data:; font-src 'self'; style-src 'self' 'unsafe-inline'"

// setAdminUISecurityHeaders attaches the browser-security headers to every Admin
// UI response: success, SPA fallback and 404 alike. Setting them at the top of
// the handler is what makes the 404 body — produced by http.NotFound further
// down — carry the same protection as the app shell.
func setAdminUISecurityHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Security-Policy", adminUIContentSecurityPolicy)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
}

func (s *Server) serveAdminUI(w http.ResponseWriter, r *http.Request) {
	setAdminUISecurityHeaders(w)
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
