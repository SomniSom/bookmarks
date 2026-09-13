package og

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/net/html"
)

type Meta struct {
	Title       string
	Description string
	Image       string
	Favicon     string
}

func Fetch(rawURL string) Meta {
	var m Meta
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return m
	}
	client := &http.Client{
		Timeout: 8 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 8 {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}
	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return m
	}
	req.Header.Set("User-Agent", "Bookmarks/1.0 (+personal bookmark manager)")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	resp, err := client.Do(req)
	if err != nil {
		m.Favicon = googleFavicon(u.Host)
		return m
	}
	defer resp.Body.Close()
	final := resp.Request.URL
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil || !utf8.Valid(body) {
		// still try parse; many pages are utf-8
	}
	m = parseHTML(string(body), final)
	if m.Title == "" {
		m.Title = final.Host
	}
	if m.Favicon == "" {
		m.Favicon = googleFavicon(final.Host)
	}
	return m
}

func googleFavicon(host string) string {
	return "https://www.google.com/s2/favicons?domain=" + url.QueryEscape(host) + "&sz=32"
}

func parseHTML(src string, base *url.URL) Meta {
	var m Meta
	z := html.NewTokenizer(strings.NewReader(src))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
			continue
		}
		tok := z.Token()
		switch tok.Data {
		case "title":
			if m.Title == "" {
				if z.Next() == html.TextToken {
					m.Title = strings.TrimSpace(z.Token().Data)
				}
			}
		case "meta":
			name, prop, content := "", "", ""
			for _, a := range tok.Attr {
				switch strings.ToLower(a.Key) {
				case "name":
					name = strings.ToLower(a.Val)
				case "property":
					prop = strings.ToLower(a.Val)
				case "content":
					content = strings.TrimSpace(a.Val)
				}
			}
			switch {
			case prop == "og:title" && content != "":
				m.Title = content
			case prop == "og:description" && content != "":
				m.Description = content
			case prop == "og:image" && content != "":
				m.Image = absURL(base, content)
			case name == "twitter:title" && m.Title == "" && content != "":
				m.Title = content
			case name == "twitter:description" && m.Description == "" && content != "":
				m.Description = content
			case name == "twitter:image" && m.Image == "" && content != "":
				m.Image = absURL(base, content)
			case name == "description" && m.Description == "" && content != "":
				m.Description = content
			}
		case "link":
			rel, href := "", ""
			for _, a := range tok.Attr {
				switch strings.ToLower(a.Key) {
				case "rel":
					rel = strings.ToLower(a.Val)
				case "href":
					href = a.Val
				}
			}
			if href != "" && (strings.Contains(rel, "icon") || rel == "shortcut icon") && m.Favicon == "" {
				m.Favicon = absURL(base, href)
			}
		}
	}
	return m
}

func absURL(base *url.URL, ref string) string {
	if base == nil {
		return ref
	}
	u, err := base.Parse(ref)
	if err != nil {
		return ref
	}
	return u.String()
}
