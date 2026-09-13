package auth

import (
	"net"
	"net/http"
	"strings"
)

func ClientIP(r *http.Request, trustedProxy bool) string {
	if trustedProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			part := strings.TrimSpace(strings.Split(xff, ",")[0])
			if part != "" {
				return part
			}
		}
		if xri := strings.TrimSpace(r.Header.Get("X-Real-IP")); xri != "" {
			return xri
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func BrowserFromUA(ua string) string {
	ua = strings.TrimSpace(ua)
	if ua == "" {
		return "Неизвестно"
	}
	switch {
	case strings.Contains(ua, "Firefox/") && !strings.Contains(ua, "Seamonkey/"):
		return "Firefox " + after(ua, "Firefox/")
	case strings.Contains(ua, "Edg/"):
		return "Edge " + after(ua, "Edg/")
	case strings.Contains(ua, "OPR/"):
		return "Opera " + after(ua, "OPR/")
	case strings.Contains(ua, "Chrome/") && !strings.Contains(ua, "Edg/") && !strings.Contains(ua, "OPR/"):
		return "Chrome " + after(ua, "Chrome/")
	case strings.Contains(ua, "Safari/") && !strings.Contains(ua, "Chrome/"):
		return "Safari " + after(ua, "Version/")
	default:
		if len(ua) > 80 {
			return ua[:80]
		}
		return ua
	}
}

func after(s, sep string) string {
	_, rest, ok := strings.Cut(s, sep)
	if !ok {
		return ""
	}
	rest = strings.TrimSpace(rest)
	end := len(rest)
	for i, c := range rest {
		if (c < '0' || c > '9') && c != '.' {
			end = i
			break
		}
	}
	return rest[:end]
}

func IsExtensionOrigin(origin string) bool {
	return strings.HasPrefix(origin, "chrome-extension://") || strings.HasPrefix(origin, "moz-extension://")
}
