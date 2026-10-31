package server

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
)

// staticFile — встроенный в бинарник файл со заранее посчитанным ETag.
type staticFile struct {
	data        []byte
	etag        string
	contentType string
}

// loadStatic читает каталог static из embed.FS один раз при старте.
func loadStatic(fsys fs.FS) (map[string]staticFile, error) {
	files := make(map[string]staticFile)
	entries, err := fs.ReadDir(fsys, "static")
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := fs.ReadFile(fsys, path.Join("static", entry.Name()))
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(data)
		files["/"+entry.Name()] = staticFile{
			data:        data,
			etag:        `"` + hex.EncodeToString(sum[:8]) + `"`,
			contentType: mime.TypeByExtension(path.Ext(entry.Name())),
		}
	}
	return files, nil
}

// serveStatic отдаёт файл с ETag и Cache-Control: сборка неизменна в пределах
// версии бинарника, поэтому кэш можно держать долго.
func (s *Server) serveStatic(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("path")
	if name == "" || strings.Contains(name, "..") {
		http.NotFound(w, r)
		return
	}
	file, ok := s.static["/"+strings.TrimPrefix(name, "/")]
	if !ok {
		http.NotFound(w, r)
		return
	}
	if match := r.Header.Get("If-None-Match"); match != "" && match == file.etag {
		w.Header().Set("ETag", file.etag)
		w.WriteHeader(http.StatusNotModified)
		return
	}
	h := w.Header()
	h.Set("ETag", file.etag)
	h.Set("Cache-Control", "public, max-age=86400")
	if file.contentType != "" {
		h.Set("Content-Type", file.contentType)
	}
	h.Set("Content-Length", strconv.Itoa(len(file.data)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(file.data)
	}
}
