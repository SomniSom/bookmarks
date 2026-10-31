package auth

import (
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHashPasswordAndVerify(t *testing.T) {
	tests := []struct {
		name     string
		password string
		wantErr  bool
	}{
		{name: "обычный пароль", password: "correct horse battery staple"},
		{name: "пароль с юникодом и спецсимволами", password: "Пароль! №%&* ()[]{}\\;:''\",.<>?/~`"},
		{name: "короткий пароль", password: "a"},
		{name: "очень длинный пароль", password: strings.Repeat("x", 1024)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hash, err := HashPassword(tt.password)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("HashPassword() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("HashPassword() error = %v", err)
			}
			if hash == "" {
				t.Fatalf("HashPassword() returned empty hash")
			}
			if !VerifyPassword(hash, tt.password) {
				t.Errorf("VerifyPassword() = false, want true (round-trip)")
			}
			if VerifyPassword(hash, tt.password+"wrong") {
				t.Errorf("VerifyPassword() = true for wrong password, want false")
			}
		})
	}
}

func TestHashReturnsArgon2idFormat(t *testing.T) {
	t.Run("формат $argon2id$v=19$m=65536,t=3,p=4$salt$hash", func(t *testing.T) {
		hash, err := HashPassword("pw")
		if err != nil {
			t.Fatalf("HashPassword() error = %v", err)
		}
		parts := strings.Split(hash, "$")
		if len(parts) != 6 {
			t.Fatalf("hash parts count = %d, want 6 (hash=%q)", len(parts), hash)
		}
		if parts[1] != "argon2id" {
			t.Errorf("algorithm = %q, want %q", parts[1], "argon2id")
		}
		if parts[2] != "v=19" {
			t.Errorf("version = %q, want %q", parts[2], "v=19")
		}
		if parts[3] != "m=65536,t=3,p=4" {
			t.Errorf("params = %q, want %q", parts[3], "m=65536,t=3,p=4")
		}
		for _, field := range []string{parts[4], parts[5]} {
			if _, err := decodeRawB64(t, field); err != nil {
				t.Errorf("field %q is not valid raw base64: %v", field, err)
			}
		}
	})
}

func TestVerifyPasswordRejectsBroken(t *testing.T) {
	valid, err := HashPassword("secret")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}

	tests := []struct {
		name string
		hash string
	}{
		{name: "пустая строка", hash: ""},
		{name: "без разделителя $", hash: "argon2idonly"},
		{name: "неправильное число частей", hash: "$argon2id$v=19$m=64,t=3,p=1$"},
		{name: "чужая схема argon2i", hash: "$argon2i$v=19$m=64,t=3,p=1$YWJj$YWJj"},
		{name: "неизвестный алгоритм", hash: "$bcrypt$10$YWJj$YWJj$YA"},
		{name: "битый base64 в соли", hash: "$argon2id$v=19$m=64,t=3,p=1$!!!not_base64!!!$YWJj"},
		{name: "битый base64 в хеше", hash: "$argon2id$v=19$m=64,t=3,p=1$YWJj$!!!not_base64!!!"},
		{name: "нечисловые параметры m", hash: "$argon2id$v=19$m=abc,t=3,p=1$YWJj$YWJj"},
		{name: "нечисловые параметры t", hash: "$argon2id$v=19$m=64,t=abc,p=1$YWJj$YWJj"},
		{name: "нечисловые параметры p", hash: "$argon2id$v=19$m=64,t=3,p=abc$YWJj$YWJj"},
		{name: "параметр без знака =", hash: "$argon2id$v=19$m64$YWJj$YWJj"},
		{name: "отрицательное значение параметра", hash: "$argon2id$v=19$m=-64,t=3,p=1$YWJj$YWJj"},
		{name: "переполнение uint", hash: "$argon2id$v=19$m=99999999999999999999,t=3,p=1$YWJj$YWJj"},
		{name: "перевёрнутый валидный хеш", hash: reverseString(valid)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("VerifyPassword() panicked for hash %q: %v", tt.hash, r)
				}
			}()
			if VerifyPassword(tt.hash, "secret") {
				t.Errorf("VerifyPassword(%q) = true, want false", tt.hash)
			}
		})
	}
}

func TestHashPasswordUniqueSalt(t *testing.T) {
	t.Run("два хеша одного пароля различаются", func(t *testing.T) {
		h1, err := HashPassword("same")
		if err != nil {
			t.Fatalf("HashPassword() error = %v", err)
		}
		h2, err := HashPassword("same")
		if err != nil {
			t.Fatalf("HashPassword() error = %v", err)
		}
		if h1 == h2 {
			t.Errorf("two hashes of same password are equal, want distinct salts")
		}
	})
}

func TestRandomToken(t *testing.T) {
	tests := []struct {
		name   string
		nbytes int
	}{
		{name: "1 байт", nbytes: 1},
		{name: "16 байт", nbytes: 16},
		{name: "32 байта", nbytes: 32},
		{name: "0 байт (пустой токен)", nbytes: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tok, err := RandomToken(tt.nbytes)
			if err != nil {
				t.Fatalf("RandomToken() error = %v", err)
			}
			if len(tok) != 2*tt.nbytes {
				t.Errorf("len(RandomToken(%d)) = %d, want %d", tt.nbytes, len(tok), 2*tt.nbytes)
			}
			if _, err := hex.DecodeString(tok); err != nil {
				t.Errorf("RandomToken(%d) = %q is not valid hex: %v", tt.nbytes, tok, err)
			}
		})
	}

	t.Run("разница двух вызовов", func(t *testing.T) {
		a, err := RandomToken(16)
		if err != nil {
			t.Fatalf("RandomToken() error = %v", err)
		}
		b, err := RandomToken(16)
		if err != nil {
			t.Fatalf("RandomToken() error = %v", err)
		}
		if a == b {
			t.Errorf("two RandomToken(16) calls returned same value %q", a)
		}
	})
}

func TestHashToken(t *testing.T) {
	tests := []struct {
		name    string
		token   string
		wantLen int
	}{
		{name: "пустой токен", token: "", wantLen: 64},
		{name: "короткий токен", token: "abc", wantLen: 64},
		{name: "длинный токен", token: strings.Repeat("t", 1000), wantLen: 64},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := HashToken(tt.token)
			if len(h) != tt.wantLen {
				t.Errorf("len(HashToken()) = %d, want %d", len(h), tt.wantLen)
			}
			if _, err := hex.DecodeString(h); err != nil {
				t.Errorf("HashToken() = %q is not valid hex: %v", h, err)
			}
		})
	}

	t.Run("детерминизм", func(t *testing.T) {
		first := HashToken("tok")
		second := HashToken("tok")
		if first != second {
			t.Errorf("HashToken() is not deterministic: %q != %q", first, second)
		}
	})

	t.Run("различие для разных входов", func(t *testing.T) {
		if HashToken("a") == HashToken("b") {
			t.Errorf("HashToken() gave same output for different inputs")
		}
	})

	t.Run("соответствие известному sha256", func(t *testing.T) {
		// sha256("hello") = 2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824
		if got := HashToken("hello"); got != "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824" {
			t.Errorf("HashToken(\"hello\") = %q, want known sha256", got)
		}
	})
}

func TestClientIP(t *testing.T) {
	tests := []struct {
		name         string
		remoteAddr   string
		xff          string
		xRealIP      string
		trustedProxy bool
		want         string
	}{
		{name: "доверенный прокси, XFF из нескольких адресов — берётся первый", remoteAddr: "10.0.0.1:1234", xff: "203.0.113.7, 198.51.100.2, 192.0.2.5", trustedProxy: true, want: "203.0.113.7"},
		{name: "доверенный прокси, XFF с пробелами вокруг первого", remoteAddr: "10.0.0.1:1234", xff: " 8.8.8.8 , 1.1.1.1", trustedProxy: true, want: "8.8.8.8"},
		{name: "доверенный прокси, только X-Real-IP", remoteAddr: "10.0.0.1:1234", xRealIP: "198.51.100.9", trustedProxy: true, want: "198.51.100.9"},
		{name: "доверенный прокси, без заголовков — fallback на RemoteAddr", remoteAddr: "10.0.0.1:1234", trustedProxy: true, want: "10.0.0.1"},
		{name: "недоверенный прокси, игнорирует XFF", remoteAddr: "10.0.0.1:1234", xff: "203.0.113.7", trustedProxy: false, want: "10.0.0.1"},
		{name: "недоверенный прокси, игнорирует X-Real-IP", remoteAddr: "10.0.0.1:1234", xRealIP: "198.51.100.9", trustedProxy: false, want: "10.0.0.1"},
		{name: "недоверенный прокси, занимает первенство XFF при trusted=false даже при заголовках", remoteAddr: "10.0.0.1:1234", xff: "203.0.113.7", xRealIP: "198.51.100.9", trustedProxy: false, want: "10.0.0.1"},
		{name: "доверенный прокси, XFF раньше X-Real-IP", remoteAddr: "10.0.0.1:1234", xff: "203.0.113.7", xRealIP: "198.51.100.9", trustedProxy: true, want: "203.0.113.7"},
		{name: "кривой RemoteAddr без порта возвращается как есть", remoteAddr: "not-a-valid-addr", trustedProxy: true, want: "not-a-valid-addr"},
		{name: "кривой RemoteAddr без порта, недоверенный прокси", remoteAddr: "garbage", trustedProxy: false, want: "garbage"},
		{name: "IPv6 адрес в скобках убирает порт", remoteAddr: "[2001:db8::1]:8080", trustedProxy: false, want: "2001:db8::1"},
		{name: "IPv6 без порта возвращается как есть", remoteAddr: "2001:db8::1", trustedProxy: false, want: "2001:db8::1"},
		{name: "пустой RemoteAddr", remoteAddr: "", trustedProxy: false, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
			r.RemoteAddr = tt.remoteAddr
			if tt.xff != "" {
				r.Header.Set("X-Forwarded-For", tt.xff)
			}
			if tt.xRealIP != "" {
				r.Header.Set("X-Real-IP", tt.xRealIP)
			}
			if got := ClientIP(r, tt.trustedProxy); got != tt.want {
				t.Errorf("ClientIP() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBrowserFromUA(t *testing.T) {
	tests := []struct {
		name string
		ua   string
		want string
	}{
		{name: "Firefox", ua: "Mozilla/5.0 (X11; Linux x86_64; rv:109.0) Gecko/20100101 Firefox/115.0", want: "Firefox 115.0"},
		{name: "Edge", ua: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36 Edg/120.0.2210.91", want: "Edge 120.0.2210.91"},
		{name: "Opera", ua: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36 OPR/106.0.0.0", want: "Opera 106.0.0.0"},
		{name: "Chrome", ua: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36", want: "Chrome 120.0.0.0"},
		{name: "Safari", ua: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.2 Safari/605.1.15", want: "Safari 17.2"},
		{name: "пустой UA", ua: "", want: "Неизвестно"},
		{name: "UA только из пробелов", ua: "   ", want: "Неизвестно"},
		{name: "мусорный короткий UA", ua: "curl/8.0.1", want: "curl/8.0.1"},
		{name: "длинный неизвестный UA обрезается до 80", ua: strings.Repeat("Z", 200), want: strings.Repeat("Z", 80)},
		{name: "неизвестный UA ровно 80 не режется", ua: strings.Repeat("Y", 80), want: strings.Repeat("Y", 80)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := BrowserFromUA(tt.ua); got != tt.want {
				t.Errorf("BrowserFromUA(%q) = %q, want %q", tt.ua, got, tt.want)
			}
		})
	}
}

func TestIsExtensionOrigin(t *testing.T) {
	tests := []struct {
		name   string
		origin string
		want   bool
	}{
		{name: "chrome-extension", origin: "chrome-extension://abcdefghijklmnop", want: true},
		{name: "moz-extension", origin: "moz-extension://abcdefghijklmnop", want: true},
		{name: "http", origin: "http://example.com", want: false},
		{name: "https", origin: "https://example.com", want: false},
		{name: "пустая строка", origin: "", want: false},
		{name: "chrome:// без -extension", origin: "chrome://settings", want: false},
		{name: "moz:// без -extension", origin: "moz://foo", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsExtensionOrigin(tt.origin); got != tt.want {
				t.Errorf("IsExtensionOrigin(%q) = %v, want %v", tt.origin, got, tt.want)
			}
		})
	}
}

func decodeRawB64(t *testing.T, s string) ([]byte, error) {
	t.Helper()
	return base64.RawStdEncoding.DecodeString(s)
}

func reverseString(s string) string {
	r := []rune(s)
	for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
		r[i], r[j] = r[j], r[i]
	}
	return string(r)
}
