package server

import (
	"compress/gzip"
	"net/http"
	"strings"
)

// compress сжимает ответы API: дерево и экспорт на большой базе — сотни килобайт.
// Статика и HTML не сжимаются: они малы и кэшируются отдельно.
func (s *Server) compress(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") ||
			!strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Add("Vary", "Accept-Encoding")
		gz := gzip.NewWriter(w)
		defer func() { _ = gz.Close() }()
		next.ServeHTTP(&gzipResponseWriter{ResponseWriter: w, gz: gz}, r)
	})
}

type gzipResponseWriter struct {
	http.ResponseWriter
	gz      *gzip.Writer
	started bool
}

func (w *gzipResponseWriter) WriteHeader(code int) {
	if !w.started {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Del("Content-Length")
		w.started = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *gzipResponseWriter) Write(b []byte) (int, error) {
	if !w.started {
		w.WriteHeader(http.StatusOK)
	}
	return w.gz.Write(b)
}

func (w *gzipResponseWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		_ = w.gz.Flush()
		flusher.Flush()
	}
}
