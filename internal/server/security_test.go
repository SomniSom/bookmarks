package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"bookmarks/internal/config"
)

func TestLoginLimiter(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	l := newLoginLimiter(2, 5*time.Minute)
	l.now = func() time.Time { return now }

	t.Run("свежий адрес допускается", func(t *testing.T) {
		if !l.Allow("10.0.0.1") {
			t.Fatal("первая попытка должна проходить")
		}
	})

	t.Run("после лимита адрес блокируется", func(t *testing.T) {
		l.Fail("10.0.0.2")
		if !l.Allow("10.0.0.2") {
			t.Fatal("одна неудача не должна блокировать")
		}
		l.Fail("10.0.0.2")
		if l.Allow("10.0.0.2") {
			t.Fatal("после двух неудач адрес должен блокироваться")
		}
	})

	t.Run("блокировка истекает по окну", func(t *testing.T) {
		l.Fail("10.0.0.3")
		l.Fail("10.0.0.3")
		if l.Allow("10.0.0.3") {
			t.Fatal("ожидалась блокировка")
		}
		now = now.Add(5*time.Minute + time.Second)
		if !l.Allow("10.0.0.3") {
			t.Fatal("после окна адрес должен разблокироваться")
		}
	})

	t.Run("успешный вход сбрасывает счётчик", func(t *testing.T) {
		l.Fail("10.0.0.4")
		l.Reset("10.0.0.4")
		l.Fail("10.0.0.4")
		if !l.Allow("10.0.0.4") {
			t.Fatal("после сброса лимит должен считаться заново")
		}
	})

	t.Run("адреса не мешают друг другу", func(t *testing.T) {
		l.Fail("10.0.0.5")
		l.Fail("10.0.0.5")
		if !l.Allow("10.0.0.6") {
			t.Fatal("блокировка одного адреса не должна влиять на другой")
		}
	})

	t.Run("nil-лимитер всегда пропускает", func(t *testing.T) {
		var nilLimiter *loginLimiter
		if !nilLimiter.Allow("x") {
			t.Fatal("nil-лимитер должен пропускать")
		}
		nilLimiter.Fail("x")
		nilLimiter.Reset("x")
	})
}

func TestSecurityHeaders(t *testing.T) {
	s := &Server{}
	h := s.securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("ok"))
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusTeapot {
		t.Fatalf("код = %d", rec.Code)
	}
	if rec.Body.String() != "ok" {
		t.Fatalf("тело = %q", rec.Body.String())
	}
	want := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
	}
	for key, value := range want {
		if got := rec.Header().Get(key); got != value {
			t.Fatalf("%s = %q, ожидалось %q", key, got, value)
		}
	}
	if csp := rec.Header().Get("Content-Security-Policy"); csp == "" {
		t.Fatal("CSP не выставлен")
	}
}

func TestCookieSecureOnlyBehindTrustedProxy(t *testing.T) {
	tests := []struct {
		name   string
		trust  bool
		header string
		secure bool
	}{
		{"без прокси и без заголовка", false, "", false},
		{"заголовок без доверия игнорируется", false, "https", false},
		{"доверенный прокси и https", true, "https", true},
		{"доверенный прокси и http", true, "http", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{cfg: config.Config{TrustedProxy: tc.trust}, secret: []byte("secret")}
			req := httptest.NewRequest(http.MethodPost, "/login", nil)
			if tc.header != "" {
				req.Header.Set("X-Forwarded-Proto", tc.header)
			}
			rec := httptest.NewRecorder()
			s.setSessionCookie(rec, req, "token")
			cookies := rec.Result().Cookies()
			if len(cookies) != 1 {
				t.Fatalf("cookie = %d", len(cookies))
			}
			if cookies[0].Secure != tc.secure {
				t.Fatalf("Secure = %v, ожидалось %v", cookies[0].Secure, tc.secure)
			}
		})
	}
}
