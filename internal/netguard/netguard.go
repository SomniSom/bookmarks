// Package netguard ограничивает исходящие запросы приватными адресами:
// закладка может указывать на localhost, а сервер живёт в контейнере с сетью.
package netguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

// ErrBlocked возвращается, когда адрес ведёт в приватную, петлевую или
// link-local сеть, либо это localhost.
var ErrBlocked = errors.New("адрес в приватной сети запрещён")

var blockPrivate atomic.Bool

// SetBlockPrivate включает запрет приватных адресов (из конфигурации).
func SetBlockPrivate(v bool) { blockPrivate.Store(v) }

// BlockPrivate сообщает, включён ли запрет.
func BlockPrivate() bool { return blockPrivate.Load() }

// CheckURL проверяет схему и хост до запроса. При выключенном запрете
// проверяется только схема.
func CheckURL(ctx context.Context, rawURL string) error {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return fmt.Errorf("url: %w", err)
	}
	switch u.Scheme {
	case "http", "https":
	default:
		return fmt.Errorf("неподдерживаемая схема %q", u.Scheme)
	}
	if u.Hostname() == "" {
		return errors.New("пустой хост")
	}
	if !blockPrivate.Load() {
		return nil
	}
	if strings.EqualFold(u.Hostname(), "localhost") || strings.HasSuffix(strings.ToLower(u.Hostname()), ".localhost") {
		return ErrBlocked
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, u.Hostname())
	if err != nil {
		return fmt.Errorf("dns: %w", err)
	}
	for _, addr := range ips {
		if IsPrivateIP(addr.IP) {
			return fmt.Errorf("%w: %s", ErrBlocked, addr.IP)
		}
	}
	return nil
}

// IsPrivateIP сообщает, относится ли адрес к приватным или служебным диапазонам.
func IsPrivateIP(ip net.IP) bool {
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() ||
		ip.IsMulticast()
}

// control проверяет адрес на уровне dial: ловит и редиректы, и DNS-rebinding,
// которые не видны при проверке исходного URL.
func control(_, address string, _ syscall.RawConn) error {
	if !blockPrivate.Load() {
		return nil
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	if ip := net.ParseIP(host); ip != nil && IsPrivateIP(ip) {
		return fmt.Errorf("%w: %s", ErrBlocked, ip)
	}
	return nil
}

// Transport — общий транспорт с keep-alive и проверкой адресов на dial.
func Transport(maxIdle, maxIdlePerHost int) *http.Transport {
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
			Control:   control,
		}).DialContext,
		MaxIdleConns:        maxIdle,
		MaxIdleConnsPerHost: maxIdlePerHost,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}
}
