package links

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"bookmarks/internal/store"
)

const waitDeadline = 15 * time.Second

// newTestChecker создаёт хранилище и чекер, закрывающиеся по завершении теста.
func newTestChecker(t *testing.T) (*Checker, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/t.sqlite")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return &Checker{Store: st}, st
}

func mustBookmark(t *testing.T, st *store.Store, title, url string) *store.Node {
	t.Helper()
	n, err := st.CreateNode(context.Background(), &store.Node{Type: "bookmark", Title: title, URL: url}, nil)
	if err != nil {
		t.Fatalf("CreateNode %q: %v", title, err)
	}
	return n
}

// waitFor крутит цикл с time.After до наступления условия или общего дедлайна.
func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(waitDeadline)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("тайм-аут ожидания: %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestStatusInitial(t *testing.T) {
	c, _ := newTestChecker(t)
	st := c.Status()
	if st.Running {
		t.Fatal("Running должен быть false до запуска")
	}
	if st.Checked != 0 {
		t.Fatalf("Checked = %d, ожидался 0", st.Checked)
	}
	if st.Total != 0 {
		t.Fatalf("Total = %d, ожидался 0", st.Total)
	}
}

func TestProbe(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		want    int
		empty   bool // пустое ли сообщение об ошибке
	}{
		{
			name: "ответ 200 на GET и HEAD",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			},
			want:  http.StatusOK,
			empty: true,
		},
		{
			name: "HEAD 405 повтор GET 200",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodHead {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				w.WriteHeader(http.StatusOK)
			},
			want:  http.StatusOK,
			empty: true,
		},
		{
			name: "HEAD 501 повтор GET 200",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodHead {
					w.WriteHeader(http.StatusNotImplemented)
					return
				}
				w.WriteHeader(http.StatusOK)
			},
			want:  http.StatusOK,
			empty: true,
		},
		{
			name: "ответ 404",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNotFound)
			},
			want:  http.StatusNotFound,
			empty: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(tc.handler)
			defer srv.Close()
			status, errMsg := probe(context.Background(), srv.URL)
			if status != tc.want {
				t.Fatalf("status = %d, ожидался %d", status, tc.want)
			}
			if tc.empty && errMsg != "" {
				t.Fatalf("errMsg = %q, ожидался пустой", errMsg)
			}
		})
	}
}

func TestProbeRedirects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/a":
			http.Redirect(w, r, "/b", http.StatusFound)
		case "/b":
			http.Redirect(w, r, "/c", http.StatusFound)
		case "/c":
			w.WriteHeader(http.StatusGone)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	status, errMsg := probe(context.Background(), srv.URL+"/a")
	if status != http.StatusGone {
		t.Fatalf("после двух редиректов status = %d, ожидался %d", status, http.StatusGone)
	}
	if errMsg != "" {
		t.Fatalf("errMsg = %q", errMsg)
	}
}

func TestProbeServerClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	url := srv.URL
	srv.Close()

	status, errMsg := probe(context.Background(), url)
	if status != 0 {
		t.Fatalf("status = %d, ожидался 0", status)
	}
	if errMsg == "" {
		t.Fatal("при сетевой ошибке errMsg должен быть непустым")
	}
}

func TestProbeCancelledContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	status, errMsg := probe(ctx, srv.URL)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("отменённый контекст вернулся слишком долго: %v", elapsed)
	}
	if status != 0 {
		t.Fatalf("status = %d, ожидался 0", status)
	}
	if errMsg == "" {
		t.Fatal("errMsg должен быть непустым при отмене контекста")
	}
}

func TestCheckOne(t *testing.T) {
	c, _ := newTestChecker(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNotImplemented)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	status, errMsg := c.CheckOne(context.Background(), srv.URL)
	if status != http.StatusOK {
		t.Fatalf("CheckOne status = %d, ожидался %d", status, http.StatusOK)
	}
	if errMsg != "" {
		t.Fatalf("CheckOne errMsg = %q", errMsg)
	}
}

func TestCheckAllEmptyDB(t *testing.T) {
	c, _ := newTestChecker(t)
	if !c.CheckAll(context.Background()) {
		t.Fatal("CheckAll на пустой базе должен вернуть true")
	}
	waitFor(t, func() bool { return !c.Status().Running }, "остановка CheckAll")
	st := c.Status()
	if st.Checked != 0 {
		t.Fatalf("Checked = %d, ожидался 0", st.Checked)
	}
	if st.Total != 0 {
		t.Fatalf("Total = %d, ожидался 0", st.Total)
	}
}

func TestCheckAllStatuses(t *testing.T) {
	c, st := newTestChecker(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/a", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/b", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	na := mustBookmark(t, st, "Good", srv.URL+"/a")
	nb := mustBookmark(t, st, "Bad", srv.URL+"/b")

	if !c.CheckAll(context.Background()) {
		t.Fatal("первый CheckAll должен вернуть true")
	}
	waitFor(t, func() bool {
		s := c.Status()
		return !s.Running && s.Checked == 2 && s.Total == 2
	}, "завершение CheckAll двух закладок")

	ctx := context.Background()
	ga, err := st.GetNode(ctx, na.ID)
	if err != nil {
		t.Fatalf("GetNode(a): %v", err)
	}
	if ga.LinkStatus == nil || *ga.LinkStatus != http.StatusOK {
		t.Fatalf("LinkStatus(a) = %v, ожидался %d", deref(ga.LinkStatus), http.StatusOK)
	}
	gb, err := st.GetNode(ctx, nb.ID)
	if err != nil {
		t.Fatalf("GetNode(b): %v", err)
	}
	if gb.LinkStatus == nil || *gb.LinkStatus != http.StatusNotFound {
		t.Fatalf("LinkStatus(b) = %v, ожидался %d", deref(gb.LinkStatus), http.StatusNotFound)
	}
}

func TestCheckAllRejectConcurrent(t *testing.T) {
	c, st := newTestChecker(t)
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	mustBookmark(t, st, "A", srv.URL)
	mustBookmark(t, st, "B", srv.URL)

	if !c.CheckAll(context.Background()) {
		t.Fatal("первый CheckAll должен вернуть true")
	}
	// Running выставляется синхронно в CheckAll, поэтому второй вызов обязан
	// увидеть идущую проверку и вернуть false.
	waitFor(t, func() bool { return c.Status().Running }, "запуск фоновой проверки")
	if c.CheckAll(context.Background()) {
		t.Fatal("второй CheckAll во время работы должен вернуть false")
	}
	waitFor(t, func() bool { return !c.Status().Running }, "завершение фоновой проверки")
}

func deref(p *int) int {
	if p == nil {
		return -1
	}
	return *p
}
