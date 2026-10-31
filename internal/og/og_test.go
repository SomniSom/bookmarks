package og

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestParseHTML(t *testing.T) {
	base := mustURL(t, "https://example.com/blog/post")

	tests := []struct {
		name string
		src  string
		base *url.URL
		want Meta
	}{
		{
			name: "полный набор og-тегов",
			src: `<html><head>
				<title>old</title>
				<meta property="og:title" content="Hello World">
				<meta property="og:description" content="Some description">
				<meta property="og:image" content="https://cdn.example.com/img.png">
				<link rel="icon" href="/favicon.ico">
			</head></html>`,
			base: base,
			want: Meta{
				Title:       "Hello World",
				Description: "Some description",
				Image:       "https://cdn.example.com/img.png",
				Favicon:     "https://example.com/favicon.ico",
			},
		},
		{
			name: "приоритет og над twitter и над title",
			src: `<html><head>
				<title>Title Tag</title>
				<meta name="twitter:title" content="Twitter Title">
				<meta property="og:title" content="OG Title">
				<meta name="twitter:description" content="Tw desc">
				<meta property="og:description" content="OG desc">
				<meta name="twitter:image" content="/tw.png">
				<meta property="og:image" content="/og.png">
			</head></html>`,
			base: base,
			want: Meta{
				Title:       "OG Title",
				Description: "OG desc",
				Image:       "https://example.com/og.png",
			},
		},
		{
			name: "twitter как fallback при отсутствии og",
			src: `<html><head>
				<meta name="twitter:title" content="Tw Title">
				<meta name="twitter:description" content="Tw desc">
				<meta name="twitter:image" content="https://cdn.example.com/tw.png">
			</head></html>`,
			base: base,
			want: Meta{
				Title:       "Tw Title",
				Description: "Tw desc",
				Image:       "https://cdn.example.com/tw.png",
			},
		},
		{
			name: "meta description как fallback",
			src: `<html><head>
				<title>Just Title</title>
				<meta name="description" content="Just a meta description">
			</head></html>`,
			base: base,
			want: Meta{
				Title:       "Just Title",
				Description: "Just a meta description",
			},
		},
		{
			name: "favicon из rel shortcut icon",
			src: `<html><head>
				<link rel="shortcut icon" href="/icon.ico">
			</head></html>`,
			base: base,
			want: Meta{Favicon: "https://example.com/icon.ico"},
		},
		{
			name: "favicon из rel icon",
			src: `<html><head>
				<link rel="icon" type="image/png" href="/img/fav.png">
			</head></html>`,
			base: base,
			want: Meta{Favicon: "https://example.com/img/fav.png"},
		},
		{
			name: "относительный og:image и относительная иконка резолвятся от base",
			src: `<html><head>
				<meta property="og:image" content="images/hero.png">
				<link rel="icon" href="favicon.ico">
			</head></html>`,
			base: base,
			want: Meta{
				Image:   "https://example.com/blog/images/hero.png",
				Favicon: "https://example.com/blog/favicon.ico",
			},
		},
		{
			name: "пустой html",
			src:  "",
			want: Meta{},
		},
		{
			name: "html без head",
			src:  `<html><body><p>No head here</p></body></html>`,
			want: Meta{},
		},
		{
			name: "кривой html с незакрытыми тегами без паники",
			src:  `<html><head><meta property="og:title" content="X"><link rel="icon" href="/i.ico"><title><meta`,
			base: base,
			want: Meta{
				Title:   "X",
				Favicon: "https://example.com/i.ico",
			},
		},
		{
			name: "base равен nil — относительные ссылки возвращаются как есть",
			src: `<html><head>
				<meta property="og:image" content="/img.png">
				<link rel="icon" href="/fav.ico">
			</head></html>`,
			base: nil,
			want: Meta{
				Image:   "/img.png",
				Favicon: "/fav.ico",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseHTML(tt.src, tt.base)
			if got != tt.want {
				t.Errorf("parseHTML() = %+v, want %+v", got, tt.want)
			}
		})
	}

	t.Run("метод не паникует на бессмысленном входе", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("parseHTML запаниковал: %v", r)
			}
		}()
		_ = parseHTML(strings.Repeat("<meta><div><br><title>", 100), base)
	})
}

func TestAbsURL(t *testing.T) {
	base := mustURL(t, "https://example.com/dir/page")
	tests := []struct {
		name string
		base *url.URL
		ref  string
		want string
	}{
		{
			name: "абсолютный url остаётся без изменений",
			base: base, ref: "https://cdn.example.org/img.png",
			want: "https://cdn.example.org/img.png",
		},
		{
			name: "относительный url резолвится от base",
			base: base, ref: "../img.png",
			want: "https://example.com/img.png",
		},
		{
			name: "протоколо-относительный url использует схему base",
			base: base, ref: "//cdn.example.org/img.png",
			want: "https://cdn.example.org/img.png",
		},
		{
			name: "мусор возвращается как есть",
			base: base, ref: " :// bad url",
			want: " :// bad url",
		},
		{
			name: "base nil возвращает ref как есть",
			base: nil, ref: "/img.png",
			want: "/img.png",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := absURL(tt.base, tt.ref); got != tt.want {
				t.Errorf("absURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGoogleFavicon(t *testing.T) {
	tests := []struct {
		name string
		host string
		want string
	}{
		{
			name: "обычный хост",
			host: "example.com",
			want: "https://www.google.com/s2/favicons?domain=example.com&sz=32",
		},
		{
			name: "хост с портом экранируется",
			host: "example.com:8080",
			want: "https://www.google.com/s2/favicons?domain=example.com%3A8080&sz=32",
		},
		{
			name: "хост с юникодом экранируется",
			host: "пример.рф",
			want: "https://www.google.com/s2/favicons?domain=%D0%BF%D1%80%D0%B8%D0%BC%D0%B5%D1%80.%D1%80%D1%84&sz=32",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := googleFavicon(tt.host); got != tt.want {
				t.Errorf("googleFavicon(%q) = %q, want %q", tt.host, got, tt.want)
			}
		})
	}
}

func TestFetch(t *testing.T) {
	t.Run("страница с og-тегами заполняет все поля", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<html><head>
				<meta property="og:title" content="Full Title">
				<meta property="og:description" content="Full desc">
				<meta property="og:image" content="https://cdn.example.com/full.png">
				<link rel="icon" href="https://cdn.example.com/fav.ico">
			</head></html>`))
		}))
		defer srv.Close()

		got := Fetch(context.Background(), srv.URL)
		want := Meta{
			Title:       "Full Title",
			Description: "Full desc",
			Image:       "https://cdn.example.com/full.png",
			Favicon:     "https://cdn.example.com/fav.ico",
		}
		if got != want {
			t.Errorf("Fetch() = %+v, want %+v", got, want)
		}
	})

	t.Run("страница без og, но с title — title из title, favicon google", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`<html><head><title>Plain Title</title></head><body>hi</body></html>`))
		}))
		defer srv.Close()

		host := srv.URL[strings.Index(srv.URL, "://")+3:]
		got := Fetch(context.Background(), srv.URL)
		want := Meta{
			Title:   "Plain Title",
			Favicon: googleFavicon(host),
		}
		if got != want {
			t.Errorf("Fetch() = %+v, want %+v", got, want)
		}
	})

	t.Run("редирект на страницу с og использует финальный url", func(t *testing.T) {
		mux := http.NewServeMux()
		srv := httptest.NewServer(mux)
		defer srv.Close()

		mux.HandleFunc("/redirect", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/final", http.StatusFound)
		})
		mux.HandleFunc("/final", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html><head>
				<meta property="og:title" content="After Redirect">
				<meta property="og:image" content="/relative-from-final.png">
				<link rel="icon" href="/rel-fav.ico">
			</head></html>`))
		})

		got := Fetch(context.Background(), srv.URL+"/redirect")
		finHost := srv.URL[strings.Index(srv.URL, "://")+3:]
		want := Meta{
			Title:   "After Redirect",
			Image:   "http://" + finHost + "/relative-from-final.png",
			Favicon: "http://" + finHost + "/rel-fav.ico",
		}
		if got != want {
			t.Errorf("Fetch() = %+v, want %+v", got, want)
		}
	})

	t.Run("rawURL без схемы/хоста возвращает пустой Meta без сети", func(t *testing.T) {
		tests := []string{"", "not a url", "javascript:alert(1)", "example.com"}
		for _, raw := range tests {
			if got := Fetch(context.Background(), raw); got != (Meta{}) {
				t.Errorf("Fetch(%q) = %+v, want пустой Meta", raw, got)
			}
		}
	})

	t.Run("недоступный сервер — google-favicon и без паники", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
		deadURL := srv.URL
		srv.Close()

		host := deadURL[strings.Index(deadURL, "://")+3:]
		got := Fetch(context.Background(), deadURL)
		want := Meta{Favicon: googleFavicon(host)}
		if got != want {
			t.Errorf("Fetch() = %+v, want %+v", got, want)
		}
	})

	t.Run("отменённый контекст — быстрый возврат без паники", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
			time.Sleep(5 * time.Second)
		}))
		defer srv.Close()

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		start := time.Now()
		got := Fetch(ctx, srv.URL)
		if time.Since(start) > time.Second {
			t.Errorf("Fetch с отменённым ctx работал слишком долго: %v", time.Since(start))
		}
		if got.Favicon == "" {
			t.Error("ожидалась google-favicon из ветки сетевой ошибки")
		}
	})

	t.Run("большой ответ не ломает парсинг", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html><head><meta property="og:title" content="Big Page">`))
			_, _ = w.Write([]byte(strings.Repeat("<!-- comment padding -->", 20000)))
			_, _ = w.Write([]byte(`<link rel="icon" href="/big.ico"></head></html>`))
		}))
		defer srv.Close()

		got := Fetch(context.Background(), srv.URL)
		wantTitle, wantFav := "Big Page", srv.URL+"/big.ico"
		if got.Title != wantTitle {
			t.Errorf("Title = %q, want %q", got.Title, wantTitle)
		}
		if got.Favicon != wantFav {
			t.Errorf("Favicon = %q, want %q", got.Favicon, wantFav)
		}
	})
}

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", s, err)
	}
	return u
}
