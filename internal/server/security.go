package server

import (
	"net/http"
	"sync"
	"time"
)

// securityHeaders закрывает типовые риски браузера: MIME-sniffing, кликджекинг,
// утечку Referer и загрузку сторонних скриптов.
func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy",
			"default-src 'self'; "+
				"img-src 'self' https: data:; "+
				"style-src 'self'; "+
				"script-src 'self'; "+
				"connect-src 'self'; "+
				"frame-ancestors 'none'; "+
				"base-uri 'self'; "+
				"form-action 'self'")
		next.ServeHTTP(w, r)
	})
}

// loginLimiter считает неудачные попытки входа с одного адреса и временно
// блокирует его. argon2id дорогой, поэтому без ограничения /login — усилитель DoS.
type loginLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	now    func() time.Time
	fails  map[string]*loginAttempt
}

type loginAttempt struct {
	count int
	until time.Time
}

func newLoginLimiter(limit int, window time.Duration) *loginLimiter {
	return &loginLimiter{
		limit:  limit,
		window: window,
		now:    time.Now,
		fails:  make(map[string]*loginAttempt),
	}
}

// Allow сообщает, можно ли пробовать пароль с этого адреса.
func (l *loginLimiter) Allow(ip string) bool {
	if l == nil {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	a, ok := l.fails[ip]
	if !ok {
		return true
	}
	return l.now().After(a.until)
}

// Fail регистрирует неудачную попытку и блокирует адрес после лимита.
func (l *loginLimiter) Fail(ip string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	a, ok := l.fails[ip]
	if !ok {
		a = &loginAttempt{}
		l.fails[ip] = a
	} else if !a.until.IsZero() && now.After(a.until) {
		a.count = 0
		a.until = time.Time{}
	}
	a.count++
	if a.count >= l.limit {
		a.count = 0
		a.until = now.Add(l.window)
	}
	if len(l.fails) > 10000 {
		for key, val := range l.fails {
			if now.After(val.until) {
				delete(l.fails, key)
			}
		}
	}
}

// Reset сбрасывает счётчик после успешного входа.
func (l *loginLimiter) Reset(ip string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, ip)
}
