package httpui

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// panelFiles are Overtide's pages, a Svelte app built from frontend/ (npm
// run build there) into dist/.
//
//go:embed all:dist
var panelFiles embed.FS

// page serves the panel's built files; any other address is one of its
// pages, which index.html shows. The built files have their content's hash
// in their names, so they're kept a year; index.html is asked again each
// time.
func (server *Server) page(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	files, err := fs.Sub(panelFiles, "dist")
	if err != nil {
		http.Error(w, "the panel isn't built", http.StatusInternalServerError)
		return
	}
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if name != "" && name != "index.html" {
		if info, err := fs.Stat(files, name); err == nil && !info.IsDir() {
			if strings.HasPrefix(name, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			http.ServeFileFS(w, r, files, name)
			return
		}
	}
	index, err := fs.ReadFile(files, "index.html")
	if err != nil {
		http.Error(w, "the panel isn't built: run npm run build in frontend/", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-cache")
	_, _ = w.Write(index)
}
