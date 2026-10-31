package links

import (
	"context"
	"io"
	"net/http"
	"sync"
	"time"

	"bookmarks/internal/netguard"
	"bookmarks/internal/store"
)

type Checker struct {
	Store   *store.Store
	mu      sync.Mutex
	running bool
	checked int
	total   int
}

type Status struct {
	Running bool `json:"running"`
	Checked int  `json:"checked"`
	Total   int  `json:"total"`
}

func (c *Checker) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Status{Running: c.running, Checked: c.checked, Total: c.total}
}

func (c *Checker) CheckOne(ctx context.Context, rawURL string) (status int, errMsg string) {
	status, errMsg = probe(ctx, rawURL)
	return
}

func (c *Checker) CheckAll(ctx context.Context) bool {
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return false
	}
	c.running = true
	c.checked = 0
	c.total = 0
	c.mu.Unlock()
	go c.run(context.WithoutCancel(ctx))
	return true
}

func (c *Checker) run(ctx context.Context) {
	defer func() {
		c.mu.Lock()
		c.running = false
		c.mu.Unlock()
	}()
	list, err := c.Store.LiveBookmarks(ctx)
	if err != nil {
		return
	}
	c.mu.Lock()
	c.total = len(list)
	c.mu.Unlock()
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for _, n := range list {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			st, msg := probe(ctx, n.URL)
			var sp *int
			if st > 0 {
				sp = &st
			}
			_ = c.Store.UpdateLink(ctx, n.ID, sp, msg)
			c.mu.Lock()
			c.checked++
			c.mu.Unlock()
		}()
	}
	wg.Wait()
}

// client общий на процесс: keep-alive и один пул соединений вместо
// нового транспорта (и TLS-хендшейка) на каждую проверяемую ссылку.
var client = &http.Client{
	Timeout:   10 * time.Second,
	Transport: netguard.Transport(64, 8),
	CheckRedirect: func(_ *http.Request, via []*http.Request) error {
		if len(via) >= 8 {
			return http.ErrUseLastResponse
		}
		return nil
	},
}

func probe(ctx context.Context, rawURL string) (int, string) {
	if err := netguard.CheckURL(ctx, rawURL); err != nil {
		return 0, err.Error()
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	do := func(method string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, method, rawURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "Bookmarks/1.0 (+link check)")
		return client.Do(req)
	}
	resp, err := do(http.MethodHead)
	if err != nil {
		resp, err = do(http.MethodGet)
		if err != nil {
			return 0, err.Error()
		}
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode == http.StatusMethodNotAllowed || resp.StatusCode == http.StatusNotImplemented {
		resp2, err := do(http.MethodGet)
		if err != nil {
			return 0, err.Error()
		}
		defer func() { _ = resp2.Body.Close() }()
		_, _ = io.Copy(io.Discard, io.LimitReader(resp2.Body, 4096))
		return resp2.StatusCode, ""
	}
	return resp.StatusCode, ""
}
