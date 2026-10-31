package server

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newStaticServer(t *testing.T) *Server {
	t.Helper()
	files, err := loadStatic(embedded)
	if err != nil {
		t.Fatalf("loadStatic: %v", err)
	}
	return &Server{static: files}
}

func TestServeStaticETag(t *testing.T) {
	s := newStaticServer(t)

	t.Run("файл отдаётся с кэшем и ETag", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/static/app.css", nil)
		req.SetPathValue("path", "app.css")
		rec := httptest.NewRecorder()
		s.serveStatic(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("код = %d", rec.Code)
		}
		if rec.Header().Get("ETag") == "" {
			t.Fatal("ETag не выставлен")
		}
		if !strings.Contains(rec.Header().Get("Cache-Control"), "max-age") {
			t.Fatalf("Cache-Control = %q", rec.Header().Get("Cache-Control"))
		}
		if !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/css") {
			t.Fatalf("Content-Type = %q", rec.Header().Get("Content-Type"))
		}
		if rec.Body.Len() == 0 {
			t.Fatal("пустое тело")
		}
	})

	t.Run("повторный запрос с ETag → 304", func(t *testing.T) {
		first := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/static/app.css", nil)
		req.SetPathValue("path", "app.css")
		s.serveStatic(first, req)
		etag := first.Header().Get("ETag")

		second := httptest.NewRecorder()
		req2 := httptest.NewRequest(http.MethodGet, "/static/app.css", nil)
		req2.SetPathValue("path", "app.css")
		req2.Header.Set("If-None-Match", etag)
		s.serveStatic(second, req2)

		if second.Code != http.StatusNotModified {
			t.Fatalf("код = %d, ожидался 304", second.Code)
		}
		if second.Body.Len() != 0 {
			t.Fatalf("тело у 304 = %q", second.Body.String())
		}
	})

	t.Run("HEAD без тела", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodHead, "/static/app.css", nil)
		req.SetPathValue("path", "app.css")
		rec := httptest.NewRecorder()
		s.serveStatic(rec, req)
		if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
			t.Fatalf("код = %d, тело = %q", rec.Code, rec.Body.String())
		}
	})

	t.Run("неизвестный файл и обход каталога → 404", func(t *testing.T) {
		for _, name := range []string{"нет-такого.css", "../server.go", "", "app.css/.."} {
			req := httptest.NewRequest(http.MethodGet, "/static/"+name, nil)
			req.SetPathValue("path", name)
			rec := httptest.NewRecorder()
			s.serveStatic(rec, req)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("%q: код = %d", name, rec.Code)
			}
		}
	})
}

func TestCompress(t *testing.T) {
	s := &Server{}
	payload := map[string]string{"hello": strings.Repeat("мир ", 200)}
	handler := s.compress(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}))

	t.Run("API сжимается при Accept-Encoding", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/tree", nil)
		req.Header.Set("Accept-Encoding", "gzip")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Header().Get("Content-Encoding") != "gzip" {
			t.Fatalf("Content-Encoding = %q", rec.Header().Get("Content-Encoding"))
		}
		if !strings.Contains(rec.Header().Get("Vary"), "Accept-Encoding") {
			t.Fatalf("Vary = %q", rec.Header().Get("Vary"))
		}
		reader, err := gzip.NewReader(rec.Body)
		if err != nil {
			t.Fatalf("gzip: %v", err)
		}
		defer func() { _ = reader.Close() }()
		body, err := io.ReadAll(reader)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		var got map[string]string
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("json: %v", err)
		}
		if got["hello"] != payload["hello"] {
			t.Fatal("тело исказилось при сжатии")
		}
	})

	t.Run("без Accept-Encoding ответ не сжат", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/tree", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Header().Get("Content-Encoding") == "gzip" {
			t.Fatal("неожиданное сжатие")
		}
		if !strings.Contains(rec.Body.String(), "мир") {
			t.Fatalf("тело = %q", rec.Body.String())
		}
	})

	t.Run("не-API путь не сжимается", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/static/app.css", nil)
		req.Header.Set("Accept-Encoding", "gzip")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Header().Get("Content-Encoding") == "gzip" {
			t.Fatal("статика не должна сжиматься этим слоем")
		}
	})
}
