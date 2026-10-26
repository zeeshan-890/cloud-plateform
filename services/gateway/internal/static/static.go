package static

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Handler serves pre-rendered static assets with SPA fallback for client-side routing.
type Handler struct {
	Dir string
	fs  http.Handler
}

func NewHandler(dir string) *Handler {
	return &Handler{
		Dir: dir,
		fs:  http.FileServer(http.Dir(dir)),
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Only serve GET and HEAD
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	cleanPath := filepath.Clean(strings.TrimPrefix(r.URL.Path, "/"))
	targetPath := filepath.Join(h.Dir, cleanPath)

	fi, err := os.Stat(targetPath)
	if err == nil {
		if fi.IsDir() {
			indexPath := filepath.Join(targetPath, "index.html")
			if _, err := os.Stat(indexPath); err == nil {
				h.fs.ServeHTTP(w, r)
				return
			}
		} else {
			// Cache static immutable assets in _next/static for 1 year
			if strings.HasPrefix(r.URL.Path, "/_next/static/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			h.fs.ServeHTTP(w, r)
			return
		}
	}

	// Try HTML file for path without extension (e.g. /projects -> /projects.html or /projects/index.html)
	htmlPath := targetPath + ".html"
	if _, err := os.Stat(htmlPath); err == nil {
		r.URL.Path = r.URL.Path + ".html"
		h.fs.ServeHTTP(w, r)
		return
	}

	// SPA Fallback: check 404.html or index.html
	fallback404 := filepath.Join(h.Dir, "404.html")
	if _, err := os.Stat(fallback404); err == nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		http.ServeFile(w, r, fallback404)
		return
	}

	fallbackIndex := filepath.Join(h.Dir, "index.html")
	if _, err := os.Stat(fallbackIndex); err == nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		http.ServeFile(w, r, fallbackIndex)
		return
	}

	http.NotFound(w, r)
}
