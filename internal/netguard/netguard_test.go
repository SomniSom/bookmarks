package netguard

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"testing"
)

func withBlock(t *testing.T, block bool) {
	t.Helper()
	previous := BlockPrivate()
	SetBlockPrivate(block)
	t.Cleanup(func() { SetBlockPrivate(previous) })
}

func TestCheckURLScheme(t *testing.T) {
	withBlock(t, false)
	tests := []struct {
		name string
		url  string
		err  bool
	}{
		{"http", "http://example.com/a", false},
		{"https", "https://example.com", false},
		{"ftp", "ftp://example.com", true},
		{"javascript", "javascript:alert(1)", true},
		{"пусто", "", true},
		{"без хоста", "https://", true},
		{"пробелы по краям", "  https://example.com  ", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckURL(context.Background(), tc.url)
			if tc.err && err == nil {
				t.Fatalf("ожидалась ошибка для %q", tc.url)
			}
			if !tc.err && err != nil {
				t.Fatalf("неожиданная ошибка для %q: %v", tc.url, err)
			}
		})
	}
}

func TestCheckURLPrivateHosts(t *testing.T) {
	withBlock(t, true)
	blocked := []string{
		"http://localhost/",
		"http://localhost:8080/path",
		"http://app.localhost/",
		"http://127.0.0.1/",
		"http://127.0.0.1:3000/",
		"http://[::1]/",
		"http://10.0.0.5/",
		"http://192.168.1.1/",
		"http://172.16.0.1/",
		"http://169.254.169.254/latest/meta-data/",
		"http://0.0.0.0/",
	}
	for _, raw := range blocked {
		t.Run(raw, func(t *testing.T) {
			err := CheckURL(context.Background(), raw)
			if !errors.Is(err, ErrBlocked) {
				t.Fatalf("ожидался ErrBlocked для %q, получено %v", raw, err)
			}
		})
	}

	allowed := []string{"http://8.8.8.8/", "https://1.1.1.1/"}
	for _, raw := range allowed {
		t.Run("разрешён "+raw, func(t *testing.T) {
			if err := CheckURL(context.Background(), raw); err != nil {
				t.Fatalf("публичный адрес %q отклонён: %v", raw, err)
			}
		})
	}
}

func TestCheckURLBlockDisabled(t *testing.T) {
	withBlock(t, false)
	for _, raw := range []string{"http://127.0.0.1/", "http://localhost/", "http://10.1.2.3/"} {
		if err := CheckURL(context.Background(), raw); err != nil {
			t.Fatalf("при выключенном запрете %q отклонён: %v", raw, err)
		}
	}
}

func TestIsPrivateIP(t *testing.T) {
	tests := []struct {
		ip      string
		private bool
	}{
		{"127.0.0.1", true},
		{"::1", true},
		{"10.0.0.1", true},
		{"192.168.0.1", true},
		{"172.16.0.1", true},
		{"169.254.169.254", true},
		{"0.0.0.0", true},
		{"224.0.0.1", true},
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"2606:4700:4700::1111", false},
	}
	for _, tc := range tests {
		t.Run(tc.ip, func(t *testing.T) {
			ip := net.ParseIP(tc.ip)
			if ip == nil {
				t.Fatalf("не разобрал %q", tc.ip)
			}
			if got := IsPrivateIP(ip); got != tc.private {
				t.Fatalf("IsPrivateIP(%s) = %v, ожидалось %v", tc.ip, got, tc.private)
			}
		})
	}
}

func TestControl(t *testing.T) {
	withBlock(t, true)
	if err := control("tcp", "127.0.0.1:80", nil); !errors.Is(err, ErrBlocked) {
		t.Fatalf("петлевой адрес не заблокирован: %v", err)
	}
	if err := control("tcp", "10.0.0.1:443", nil); !errors.Is(err, ErrBlocked) {
		t.Fatalf("приватный адрес не заблокирован: %v", err)
	}
	if err := control("tcp", "8.8.8.8:53", nil); err != nil {
		t.Fatalf("публичный адрес заблокирован: %v", err)
	}
	if err := control("tcp", "без-порта", nil); err == nil {
		t.Fatal("адрес без порта должен давать ошибку")
	}

	withBlock(t, false)
	if err := control("tcp", "127.0.0.1:80", nil); err != nil {
		t.Fatalf("при выключенном запрете dial заблокирован: %v", err)
	}
}

func TestTransport(t *testing.T) {
	tr := Transport(32, 4)
	if tr == nil || tr.DialContext == nil {
		t.Fatal("транспорт должен иметь DialContext")
	}
	if tr.MaxIdleConns != 32 || tr.MaxIdleConnsPerHost != 4 {
		t.Fatalf("лимиты не применились: %+v", tr)
	}
	if _, ok := any(tr).(*http.Transport); !ok {
		t.Fatal("ожидался *http.Transport")
	}
	if u, err := url.Parse("https://example.com"); err != nil || u.Host == "" {
		t.Fatalf("url: %v", err)
	}
}
