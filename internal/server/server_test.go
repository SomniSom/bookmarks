package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"bookmarks/internal/config"
	"bookmarks/internal/store"
)

const testPassword = "пароль-из-тестов"

type testEnv struct {
	st     *store.Store
	ts     *httptest.Server
	client *http.Client
}

type testResp struct {
	Code   int
	Header http.Header
	Body   []byte
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.sqlite"))
	if err != nil {
		t.Fatalf("открытие хранилища: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.EnsurePassword(context.Background(), testPassword); err != nil {
		t.Fatalf("EnsurePassword: %v", err)
	}
	srv, err := New(config.Config{Listen: "127.0.0.1:0"}, st)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New: %v", err)
	}
	return &testEnv{
		st: st,
		ts: ts,
		client: &http.Client{
			Jar:     jar,
			Timeout: 10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

func (e *testEnv) call(t *testing.T, method, path string, body []byte, mod func(*http.Request)) *testResp {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, e.ts.URL+path, rdr)
	if err != nil {
		t.Fatalf("%s %s: сборка запроса: %v", method, path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if mod != nil {
		mod(req)
	}
	res, err := e.client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: запрос: %v", method, path, err)
	}
	defer func() { _ = res.Body.Close() }()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("%s %s: чтение тела: %v", method, path, err)
	}
	return &testResp{Code: res.StatusCode, Header: res.Header.Clone(), Body: data}
}

func (e *testEnv) get(t *testing.T, path string) *testResp {
	t.Helper()
	return e.call(t, http.MethodGet, path, nil, nil)
}

func (e *testEnv) sendJSON(t *testing.T, method, path string, payload any) *testResp {
	t.Helper()
	var body []byte
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("%s %s: кодирование тела: %v", method, path, err)
		}
		body = b
	}
	return e.call(t, method, path, body, nil)
}

func (e *testEnv) postForm(t *testing.T, path string, form url.Values) *testResp {
	t.Helper()
	return e.call(t, http.MethodPost, path, []byte(form.Encode()), func(r *http.Request) {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	})
}

func (e *testEnv) login(t *testing.T, password string) *testResp {
	t.Helper()
	return e.postForm(t, "/login", url.Values{"password": {password}})
}

func (e *testEnv) mustLogin(t *testing.T) {
	t.Helper()
	r := e.login(t, testPassword)
	requireRedirect(t, r, http.StatusSeeOther, "/")
	if cookieFrom(r, cookieName) == nil {
		t.Fatalf("после входа сервер не выставил cookie %q", cookieName)
	}
}

func (e *testEnv) sessionCookie(t *testing.T) string {
	t.Helper()
	u, err := url.Parse(e.ts.URL)
	if err != nil {
		t.Fatalf("разбор адреса тестового сервера: %v", err)
	}
	for _, c := range e.client.Jar.Cookies(u) {
		if c.Name == cookieName {
			return c.Value
		}
	}
	t.Fatalf("в jar нет cookie %q", cookieName)
	return ""
}

func (e *testEnv) createNode(t *testing.T, payload map[string]any) *store.Node {
	t.Helper()
	r := e.sendJSON(t, http.MethodPost, "/api/nodes", payload)
	requireStatus(t, r, http.StatusCreated)
	var n store.Node
	r.decode(t, &n)
	if n.ID == "" {
		t.Fatalf("сервер не вернул id созданного узла: %s", r.Body)
	}
	return &n
}

func (e *testEnv) createFolder(t *testing.T, title string, parentID *string) *store.Node {
	t.Helper()
	payload := map[string]any{"type": "folder", "title": title}
	if parentID != nil {
		payload["parentId"] = *parentID
	}
	return e.createNode(t, payload)
}

func (e *testEnv) createBookmark(t *testing.T, title, rawURL string, parentID *string) *store.Node {
	t.Helper()
	payload := map[string]any{"type": "bookmark", "title": title, "url": rawURL, "fetchOG": false}
	if parentID != nil {
		payload["parentId"] = *parentID
	}
	return e.createNode(t, payload)
}

func (e *testEnv) tree(t *testing.T) []*store.Node {
	t.Helper()
	r := e.get(t, "/api/tree")
	requireStatus(t, r, http.StatusOK)
	var list []*store.Node
	r.decode(t, &list)
	return list
}

func (e *testEnv) trash(t *testing.T) []*store.Node {
	t.Helper()
	r := e.get(t, "/api/trash")
	requireStatus(t, r, http.StatusOK)
	var list []*store.Node
	r.decode(t, &list)
	return list
}

func (r *testResp) text() string {
	return string(r.Body)
}

func (r *testResp) decode(t *testing.T, v any) {
	t.Helper()
	if err := json.Unmarshal(r.Body, v); err != nil {
		t.Fatalf("тело не JSON (код %d): %v; тело: %s", r.Code, err, r.Body)
	}
}

func cookiesOf(r *testResp) []*http.Cookie {
	return (&http.Response{Header: r.Header}).Cookies()
}

func cookieFrom(r *testResp, name string) *http.Cookie {
	for _, c := range cookiesOf(r) {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func requireStatus(t *testing.T, r *testResp, want int) {
	t.Helper()
	if r.Code != want {
		t.Fatalf("код %d, ожидался %d; тело: %s", r.Code, want, r.Body)
	}
}

func requireRedirect(t *testing.T, r *testResp, wantCode int, wantLoc string) {
	t.Helper()
	requireStatus(t, r, wantCode)
	if got := r.Header.Get("Location"); got != wantLoc {
		t.Fatalf("Location = %q, ожидался %q", got, wantLoc)
	}
}

func requireContentType(t *testing.T, r *testResp, want string) {
	t.Helper()
	if got := r.Header.Get("Content-Type"); got != want {
		t.Fatalf("Content-Type = %q, ожидался %q", got, want)
	}
}

func requireJSONError(t *testing.T, r *testResp) string {
	t.Helper()
	var body map[string]string
	r.decode(t, &body)
	if body["error"] == "" {
		t.Fatalf("в ответе нет поля error: %s", r.Body)
	}
	return body["error"]
}

func bearerAuth(token, device string) func(*http.Request) {
	return func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("X-Device-Id", device)
	}
}

func withOrigin(origin string) func(*http.Request) {
	return func(r *http.Request) {
		r.Header.Set("Origin", origin)
	}
}

func findNode(list []*store.Node, id string) *store.Node {
	for _, n := range list {
		if n.ID == id {
			return n
		}
		if found := findNode(n.Children, id); found != nil {
			return found
		}
	}
	return nil
}

func TestPages(t *testing.T) {
	t.Run("страница входа отдаёт форму", func(t *testing.T) {
		e := newTestEnv(t)
		r := e.get(t, "/login")
		requireStatus(t, r, http.StatusOK)
		requireContentType(t, r, "text/html; charset=utf-8")
		body := r.text()
		for _, want := range []string{`action="/login"`, `name="password"`, "Войти"} {
			if !strings.Contains(body, want) {
				t.Errorf("в теле страницы входа нет %q", want)
			}
		}
		if r.Header.Get("Content-Length") == "" {
			t.Error("сервер не выставил Content-Length у страницы")
		}
	})

	t.Run("закрытые страницы перенаправляют на вход", func(t *testing.T) {
		e := newTestEnv(t)
		for _, path := range []string{"/", "/trash", "/settings"} {
			t.Run("GET "+path, func(t *testing.T) {
				r := e.get(t, path)
				requireRedirect(t, r, http.StatusFound, "/login")
			})
		}
	})

	t.Run("после входа страницы отдаются", func(t *testing.T) {
		e := newTestEnv(t)
		e.mustLogin(t)
		for _, path := range []string{"/", "/trash", "/settings"} {
			t.Run("GET "+path, func(t *testing.T) {
				r := e.get(t, path)
				requireStatus(t, r, http.StatusOK)
				requireContentType(t, r, "text/html; charset=utf-8")
				if len(r.Body) == 0 {
					t.Fatal("пустое тело страницы")
				}
			})
		}
	})

	t.Run("неизвестный путь отдаёт 404 после входа", func(t *testing.T) {
		e := newTestEnv(t)
		e.mustLogin(t)
		r := e.get(t, "/нет-такой-страницы")
		requireStatus(t, r, http.StatusNotFound)
	})
}

func TestLoginAndLogout(t *testing.T) {
	t.Run("неверный пароль", func(t *testing.T) {
		e := newTestEnv(t)
		r := e.login(t, "совсем-не-тот-пароль")
		requireRedirect(t, r, http.StatusSeeOther, "/login?e=1")
		if c := cookieFrom(r, cookieName); c != nil {
			t.Fatalf("при неверном пароле выставлена cookie сессии: %v", c)
		}
	})

	t.Run("верный пароль выставляет подписанную cookie", func(t *testing.T) {
		e := newTestEnv(t)
		r := e.login(t, testPassword)
		requireRedirect(t, r, http.StatusSeeOther, "/")
		c := cookieFrom(r, cookieName)
		if c == nil {
			t.Fatalf("нет cookie %q: %v", cookieName, r.Header.Values("Set-Cookie"))
		}
		if !c.HttpOnly {
			t.Error("cookie сессии должна быть HttpOnly")
		}
		if !strings.Contains(c.Value, ".") {
			t.Errorf("значение cookie не похоже на подписанное: %q", c.Value)
		}
		if c.SameSite != http.SameSiteLaxMode {
			t.Errorf("SameSite = %v, ожидался Lax", c.SameSite)
		}
	})

	t.Run("страница входа с cookie перенаправляет в кабинет", func(t *testing.T) {
		e := newTestEnv(t)
		e.mustLogin(t)
		r := e.get(t, "/login")
		requireRedirect(t, r, http.StatusFound, "/")
	})

	t.Run("выход завершает сессию", func(t *testing.T) {
		e := newTestEnv(t)
		e.mustLogin(t)
		saved := e.sessionCookie(t)

		r := e.call(t, http.MethodPost, "/logout", nil, nil)
		requireRedirect(t, r, http.StatusSeeOther, "/login")
		if c := cookieFrom(r, cookieName); c == nil || c.MaxAge >= 0 {
			t.Fatalf("выход не сбросил cookie: %v", r.Header.Values("Set-Cookie"))
		}

		stale := e.call(t, http.MethodGet, "/api/tree", nil, func(req *http.Request) {
			req.AddCookie(&http.Cookie{Name: cookieName, Value: saved})
		})
		requireStatus(t, stale, http.StatusUnauthorized)

		page := e.get(t, "/")
		requireRedirect(t, page, http.StatusFound, "/login")
	})
}

func TestAPIAuthorization(t *testing.T) {
	t.Run("без авторизации 401 и JSON с error", func(t *testing.T) {
		e := newTestEnv(t)
		r := e.get(t, "/api/tree")
		requireStatus(t, r, http.StatusUnauthorized)
		requireContentType(t, r, "application/json; charset=utf-8")
		if msg := requireJSONError(t, r); msg != "unauthorized" {
			t.Errorf("error = %q, ожидалось %q", msg, "unauthorized")
		}
	})

	t.Run("подделанная cookie не даёт доступа", func(t *testing.T) {
		e := newTestEnv(t)
		r := e.call(t, http.MethodGet, "/api/tree", nil, func(req *http.Request) {
			req.AddCookie(&http.Cookie{Name: cookieName, Value: "подделка.подпись"})
		})
		requireStatus(t, r, http.StatusUnauthorized)
	})

	t.Run("пустая база отдаёт пустые массивы вместо null", func(t *testing.T) {
		e := newTestEnv(t)
		e.mustLogin(t)
		for _, path := range []string{"/api/tree", "/api/search?q=", "/api/tags", "/api/trash", "/api/duplicates?url="} {
			r := e.get(t, path)
			requireStatus(t, r, http.StatusOK)
			requireContentType(t, r, "application/json; charset=utf-8")
			if got := strings.TrimSpace(r.text()); got != "[]" {
				t.Errorf("%s: тело %q, ожидался пустой массив", path, got)
			}
		}
	})

	t.Run("веб-API без cookie отвечает 401", func(t *testing.T) {
		e := newTestEnv(t)
		for _, tc := range []struct{ method, path string }{
			{http.MethodGet, "/api/trash"},
			{http.MethodGet, "/api/settings"},
			{http.MethodGet, "/api/export"},
			{http.MethodGet, "/api/nodes/unknown"},
			{http.MethodGet, "/api/ext/sessions"},
			{http.MethodGet, "/api/links/status"},
			{http.MethodPatch, "/api/settings"},
			{http.MethodDelete, "/api/trash"},
		} {
			r := e.sendJSON(t, tc.method, tc.path, map[string]any{})
			requireStatus(t, r, http.StatusUnauthorized)
			if msg := requireJSONError(t, r); msg != "unauthorized" {
				t.Errorf("%s %s: error = %q", tc.method, tc.path, msg)
			}
		}
	})
}

func TestNodesCRUD(t *testing.T) {
	e := newTestEnv(t)
	e.mustLogin(t)

	folder := e.createFolder(t, "Технологии", nil)
	bookmark := e.createBookmark(t, "Пример", "example.com", &folder.ID)

	t.Run("url нормализуется при создании", func(t *testing.T) {
		if bookmark.URL != "https://example.com" {
			t.Fatalf("URL = %q, ожидался %q", bookmark.URL, "https://example.com")
		}
		if bookmark.ParentID == nil || *bookmark.ParentID != folder.ID {
			t.Fatalf("parentId = %v, ожидался %q", bookmark.ParentID, folder.ID)
		}
	})

	t.Run("дерево содержит папку и закладку", func(t *testing.T) {
		tree := e.tree(t)
		root := findNode(tree, folder.ID)
		if root == nil {
			t.Fatal("папки нет в дереве")
		}
		if findNode(root.Children, bookmark.ID) == nil {
			t.Fatalf("закладки нет среди детей папки: %+v", root.Children)
		}
	})

	t.Run("правка заголовка", func(t *testing.T) {
		r := e.sendJSON(t, http.MethodPatch, "/api/nodes/"+bookmark.ID, map[string]any{"title": "Новый заголовок"})
		requireStatus(t, r, http.StatusOK)
		var n store.Node
		r.decode(t, &n)
		if n.Title != "Новый заголовок" {
			t.Fatalf("title = %q, ожидался %q", n.Title, "Новый заголовок")
		}
		if got := findNode(e.tree(t), bookmark.ID); got == nil || got.Title != "Новый заголовок" {
			t.Fatalf("в дереве заголовок не изменился: %+v", got)
		}
	})

	t.Run("дубликат url без подтверждения отклоняется", func(t *testing.T) {
		r := e.sendJSON(t, http.MethodPost, "/api/nodes", map[string]any{
			"type": "bookmark", "title": "Дубль", "url": "https://example.com", "fetchOG": false,
		})
		requireStatus(t, r, http.StatusConflict)
		var body struct {
			Error      string            `json:"error"`
			Duplicates []store.Duplicate `json:"duplicates"`
		}
		r.decode(t, &body)
		if len(body.Duplicates) == 0 {
			t.Fatalf("в ответе нет непустого duplicates: %s", r.Body)
		}
		if body.Duplicates[0].ID != bookmark.ID {
			t.Fatalf("duplicates[0].id = %q, ожидался %q", body.Duplicates[0].ID, bookmark.ID)
		}
	})

	t.Run("дубликат url с подтверждением создаётся", func(t *testing.T) {
		dup := e.createNode(t, map[string]any{
			"type": "bookmark", "title": "Дубль", "url": "https://example.com",
			"fetchOG": false, "confirmDuplicate": true,
		})
		if dup.ID == bookmark.ID {
			t.Fatal("подтверждённый дубликат получил тот же id")
		}
	})

	t.Run("удаление, восстановление и окончательное удаление", func(t *testing.T) {
		n := e.createBookmark(t, "Удаляемая", "https://delete.example", &folder.ID)

		requireStatus(t, e.call(t, http.MethodDelete, "/api/nodes/"+n.ID, nil, nil), http.StatusOK)
		if findNode(e.trash(t), n.ID) == nil {
			t.Fatal("удалённого узла нет в корзине")
		}
		if findNode(e.tree(t), n.ID) != nil {
			t.Fatal("удалённый узел остался в дереве")
		}

		requireStatus(t, e.call(t, http.MethodPost, "/api/nodes/"+n.ID+"/restore", nil, nil), http.StatusOK)
		if findNode(e.tree(t), n.ID) == nil {
			t.Fatal("восстановленный узел не вернулся в дерево")
		}

		requireStatus(t, e.call(t, http.MethodDelete, "/api/nodes/"+n.ID, nil, nil), http.StatusOK)
		requireStatus(t, e.call(t, http.MethodDelete, "/api/nodes/"+n.ID+"/purge", nil, nil), http.StatusOK)
		requireStatus(t, e.get(t, "/api/nodes/"+n.ID), http.StatusNotFound)
		if findNode(e.trash(t), n.ID) != nil {
			t.Fatal("после purge узел остался в корзине")
		}
	})

	t.Run("move переносит закладку между папками", func(t *testing.T) {
		target := e.createFolder(t, "Архив", nil)
		requireStatus(t, e.sendJSON(t, http.MethodPost, "/api/nodes/"+bookmark.ID+"/move", map[string]any{
			"parentId": target.ID,
		}), http.StatusOK)
		moved := findNode(e.tree(t), bookmark.ID)
		if moved == nil || moved.ParentID == nil || *moved.ParentID != target.ID {
			t.Fatalf("закладка не перенесена: %+v", moved)
		}
	})
}

func TestNodeParentPatch(t *testing.T) {
	e := newTestEnv(t)
	e.mustLogin(t)

	alpha := e.createFolder(t, "Альфа", nil)
	beta := e.createFolder(t, "Бета", nil)
	bookmark := e.createBookmark(t, "Переносимая", "https://move.example", &alpha.ID)

	t.Run("перенос закладки в другую папку", func(t *testing.T) {
		r := e.sendJSON(t, http.MethodPatch, "/api/nodes/"+bookmark.ID, map[string]any{"parentId": beta.ID})
		requireStatus(t, r, http.StatusOK)
		var n store.Node
		r.decode(t, &n)
		if n.ParentID == nil || *n.ParentID != beta.ID {
			t.Fatalf("parentId = %v, ожидался %q", n.ParentID, beta.ID)
		}
		if found := findNode(e.tree(t), beta.ID); found == nil || findNode(found.Children, bookmark.ID) == nil {
			t.Fatal("закладка не оказалась в папке «Бета»")
		}
	})

	t.Run("вложение папки в собственного потомка запрещено", func(t *testing.T) {
		child := e.createFolder(t, "Дочерняя", &alpha.ID)
		r := e.sendJSON(t, http.MethodPatch, "/api/nodes/"+alpha.ID, map[string]any{"parentId": child.ID})
		requireStatus(t, r, http.StatusBadRequest)
		if msg := requireJSONError(t, r); !strings.Contains(msg, "потомк") {
			t.Fatalf("сообщение %q не упоминает потомка", msg)
		}
	})

	t.Run("закладка не может быть родителем", func(t *testing.T) {
		other := e.createBookmark(t, "Другая", "https://other.example", nil)
		r := e.sendJSON(t, http.MethodPatch, "/api/nodes/"+bookmark.ID, map[string]any{"parentId": other.ID})
		requireStatus(t, r, http.StatusBadRequest)
		if msg := requireJSONError(t, r); !strings.Contains(msg, "недопустимая") {
			t.Fatalf("сообщение %q не описывает недопустимого родителя", msg)
		}
	})
}

func TestExtensionAuth(t *testing.T) {
	t.Run("без deviceId вход отклоняется", func(t *testing.T) {
		e := newTestEnv(t)
		r := e.sendJSON(t, http.MethodPost, "/api/ext/login", map[string]any{"password": testPassword})
		requireStatus(t, r, http.StatusUnauthorized)
		requireJSONError(t, r)
	})

	t.Run("неверный пароль отклоняется", func(t *testing.T) {
		e := newTestEnv(t)
		r := e.sendJSON(t, http.MethodPost, "/api/ext/login", map[string]any{
			"password": "чужой-пароль", "deviceId": "device-1",
		})
		requireStatus(t, r, http.StatusUnauthorized)
		requireJSONError(t, r)
	})

	t.Run("токен привязан к устройству", func(t *testing.T) {
		e := newTestEnv(t)
		device := "device-1"
		r := e.sendJSON(t, http.MethodPost, "/api/ext/login", map[string]any{
			"password": testPassword, "deviceId": device,
		})
		requireStatus(t, r, http.StatusOK)
		var body struct {
			Token   string            `json:"token"`
			Session *store.ExtSession `json:"session"`
		}
		r.decode(t, &body)
		if body.Token == "" {
			t.Fatalf("сервер не вернул token: %s", r.Body)
		}
		if body.Session == nil || body.Session.DeviceID != device {
			t.Fatalf("сессия не привязана к устройству: %+v", body.Session)
		}

		ok := e.call(t, http.MethodGet, "/api/tree", nil, bearerAuth(body.Token, device))
		requireStatus(t, ok, http.StatusOK)
		if got := strings.TrimSpace(ok.text()); got != "[]" {
			t.Fatalf("дерево расширения = %q, ожидался пустой массив", got)
		}

		wrongDevice := e.call(t, http.MethodGet, "/api/tree", nil, bearerAuth(body.Token, "device-2"))
		requireStatus(t, wrongDevice, http.StatusUnauthorized)

		noDevice := e.call(t, http.MethodGet, "/api/tree", nil, func(req *http.Request) {
			req.Header.Set("Authorization", "Bearer "+body.Token)
		})
		requireStatus(t, noDevice, http.StatusUnauthorized)
	})

	t.Run("открытый режим разрешён только при require_extension_auth=0", func(t *testing.T) {
		e := newTestEnv(t)
		origin := withOrigin("chrome-extension://abcdefghijklmnop")

		requireStatus(t, e.call(t, http.MethodGet, "/api/tree", nil, origin), http.StatusUnauthorized)

		if err := e.st.SetSetting(context.Background(), "require_extension_auth", "0"); err != nil {
			t.Fatalf("SetSetting: %v", err)
		}
		requireStatus(t, e.call(t, http.MethodGet, "/api/tree", nil, origin), http.StatusOK)

		plain := e.call(t, http.MethodGet, "/api/tree", nil, nil)
		requireStatus(t, plain, http.StatusUnauthorized)
	})
}

func TestCORS(t *testing.T) {
	e := newTestEnv(t)

	t.Run("preflight от расширения", func(t *testing.T) {
		origin := "chrome-extension://abcdefghijklmnop"
		r := e.call(t, http.MethodOptions, "/api/tree", nil, withOrigin(origin))
		requireStatus(t, r, http.StatusNoContent)
		if got := r.Header.Get("Access-Control-Allow-Origin"); got != origin {
			t.Fatalf("Access-Control-Allow-Origin = %q, ожидался %q", got, origin)
		}
		if got := r.Header.Get("Access-Control-Allow-Methods"); !strings.Contains(got, "PATCH") {
			t.Errorf("Access-Control-Allow-Methods = %q, нет PATCH", got)
		}
		if got := r.Header.Get("Access-Control-Allow-Headers"); !strings.Contains(got, "X-Device-Id") {
			t.Errorf("Access-Control-Allow-Headers = %q, нет X-Device-Id", got)
		}
		if got := r.Header.Get("Vary"); !strings.Contains(got, "Origin") {
			t.Errorf("Vary = %q, нет Origin", got)
		}
	})

	t.Run("firefox origin тоже разрешён", func(t *testing.T) {
		origin := "moz-extension://abcdefghijklmnop"
		r := e.call(t, http.MethodOptions, "/api/tree", nil, withOrigin(origin))
		requireStatus(t, r, http.StatusNoContent)
		if got := r.Header.Get("Access-Control-Allow-Origin"); got != origin {
			t.Fatalf("Access-Control-Allow-Origin = %q, ожидался %q", got, origin)
		}
	})

	t.Run("чужой origin не получает CORS-заголовков", func(t *testing.T) {
		r := e.call(t, http.MethodOptions, "/api/tree", nil, withOrigin("https://evil.example"))
		requireStatus(t, r, http.StatusNoContent)
		if got := r.Header.Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("чужому origin выдан Access-Control-Allow-Origin = %q", got)
		}
	})
}

func TestExportImportAndSettings(t *testing.T) {
	e := newTestEnv(t)
	e.mustLogin(t)
	e.createBookmark(t, "Экспортируемая", "https://export.example", nil)

	t.Run("экспорт отдаёт bookmarks.json", func(t *testing.T) {
		r := e.get(t, "/api/export")
		requireStatus(t, r, http.StatusOK)
		if cd := r.Header.Get("Content-Disposition"); !strings.Contains(cd, "bookmarks.json") {
			t.Fatalf("Content-Disposition = %q, нет bookmarks.json", cd)
		}
		var doc struct {
			Nodes []json.RawMessage `json:"nodes"`
		}
		r.decode(t, &doc)
		if len(doc.Nodes) == 0 {
			t.Fatalf("в экспорте нет узлов: %s", r.Body)
		}
	})

	t.Run("импорт добавляет узлы", func(t *testing.T) {
		payload := map[string]any{"nodes": []any{
			map[string]any{
				"type":  "folder",
				"title": "Импортированная папка",
				"children": []any{
					map[string]any{"type": "bookmark", "title": "Импортированная закладка", "url": "https://import.example"},
				},
			},
		}}
		r := e.sendJSON(t, http.MethodPost, "/api/import", payload)
		requireStatus(t, r, http.StatusOK)
		var ok map[string]bool
		r.decode(t, &ok)
		if !ok["ok"] {
			t.Fatalf("ответ импорта: %s", r.Body)
		}

		var imported *store.Node
		for _, n := range e.tree(t) {
			if n.Title == "Импортированная папка" {
				imported = n
			}
		}
		if imported == nil {
			t.Fatal("импортированной папки нет в дереве")
		}
		if len(imported.Children) != 1 || imported.Children[0].URL != "https://import.example" {
			t.Fatalf("импортированные дети: %+v", imported.Children)
		}
	})

	t.Run("настройки читаются и меняются", func(t *testing.T) {
		r := e.get(t, "/api/settings")
		requireStatus(t, r, http.StatusOK)
		var got map[string]any
		r.decode(t, &got)
		reqAuth, ok := got["requireExtensionAuth"].(bool)
		if !ok {
			t.Fatalf("нет булева requireExtensionAuth: %s", r.Body)
		}
		if !reqAuth {
			t.Error("по умолчанию requireExtensionAuth должен быть true")
		}
		if _, ok := got["backupDir"].(string); !ok {
			t.Fatalf("нет строкового backupDir: %s", r.Body)
		}
		if got["listen"] != "127.0.0.1:0" {
			t.Errorf("listen = %v, ожидался %q", got["listen"], "127.0.0.1:0")
		}
		if _, ok := got["extensionSessions"].([]any); !ok {
			t.Errorf("extensionSessions не массив: %T", got["extensionSessions"])
		}

		r = e.sendJSON(t, http.MethodPatch, "/api/settings", map[string]any{
			"requireExtensionAuth": false,
			"backupDir":            "/srv/backups",
		})
		requireStatus(t, r, http.StatusOK)
		var patched map[string]any
		r.decode(t, &patched)
		if patched["requireExtensionAuth"] != false {
			t.Fatalf("requireExtensionAuth = %v, ожидалось false", patched["requireExtensionAuth"])
		}
		if patched["backupDir"] != "/srv/backups" {
			t.Fatalf("backupDir = %v, ожидался %q", patched["backupDir"], "/srv/backups")
		}
	})
}

func TestLinkStatus(t *testing.T) {
	e := newTestEnv(t)
	e.mustLogin(t)

	r := e.get(t, "/api/links/status")
	requireStatus(t, r, http.StatusOK)
	requireContentType(t, r, "application/json; charset=utf-8")
	var st struct {
		Running bool `json:"running"`
		Checked int  `json:"checked"`
		Total   int  `json:"total"`
	}
	r.decode(t, &st)
	if st.Running {
		t.Fatal("до запуска running должен быть false")
	}
	if st.Checked != 0 || st.Total != 0 {
		t.Fatalf("checked = %d, total = %d, ожидались нули", st.Checked, st.Total)
	}
}

func TestBadJSON(t *testing.T) {
	e := newTestEnv(t)
	e.mustLogin(t)

	t.Run("создание узла с битым json", func(t *testing.T) {
		r := e.call(t, http.MethodPost, "/api/nodes", []byte(`{"type":`), nil)
		requireStatus(t, r, http.StatusBadRequest)
		if msg := requireJSONError(t, r); msg != "bad json" {
			t.Errorf("error = %q, ожидалось %q", msg, "bad json")
		}
	})

	t.Run("патч узла с битым json", func(t *testing.T) {
		n := e.createFolder(t, "Папка", nil)
		r := e.call(t, http.MethodPatch, "/api/nodes/"+n.ID, []byte(`не json`), nil)
		requireStatus(t, r, http.StatusBadRequest)
		requireJSONError(t, r)
	})

	t.Run("вход расширения с битым json", func(t *testing.T) {
		r := e.call(t, http.MethodPost, "/api/ext/login", []byte(`{`), nil)
		requireStatus(t, r, http.StatusBadRequest)
		requireJSONError(t, r)
	})
}

func TestTagsCRUD(t *testing.T) {
	e := newTestEnv(t)
	e.mustLogin(t)

	r := e.sendJSON(t, http.MethodPost, "/api/tags", map[string]any{"name": "go"})
	requireStatus(t, r, http.StatusCreated)
	var tag store.Tag
	r.decode(t, &tag)
	if tag.ID == "" || tag.Name != "go" {
		t.Fatalf("созданный тег: %+v", tag)
	}

	t.Run("тег появляется в списке", func(t *testing.T) {
		list := e.get(t, "/api/tags")
		requireStatus(t, list, http.StatusOK)
		var tags []store.Tag
		list.decode(t, &tags)
		if len(tags) != 1 || tags[0].ID != tag.ID || tags[0].Name != "go" {
			t.Fatalf("список тегов: %+v", tags)
		}
	})

	t.Run("переименование тега", func(t *testing.T) {
		r := e.sendJSON(t, http.MethodPatch, "/api/tags/"+tag.ID, map[string]any{"name": "Go"})
		requireStatus(t, r, http.StatusOK)
		list := e.get(t, "/api/tags")
		requireStatus(t, list, http.StatusOK)
		var tags []store.Tag
		list.decode(t, &tags)
		if len(tags) != 1 || tags[0].Name != "Go" {
			t.Fatalf("после переименования: %+v", tags)
		}
	})

	t.Run("удаление тега", func(t *testing.T) {
		requireStatus(t, e.call(t, http.MethodDelete, "/api/tags/"+tag.ID, nil, nil), http.StatusOK)
		list := e.get(t, "/api/tags")
		requireStatus(t, list, http.StatusOK)
		if got := strings.TrimSpace(list.text()); got != "[]" {
			t.Fatalf("после удаления тело %q, ожидался пустой массив", got)
		}
	})
}

func TestEmptyTrash(t *testing.T) {
	e := newTestEnv(t)
	e.mustLogin(t)

	n := e.createBookmark(t, "Мусор", "https://trash.example", nil)
	requireStatus(t, e.call(t, http.MethodDelete, "/api/nodes/"+n.ID, nil, nil), http.StatusOK)
	if findNode(e.trash(t), n.ID) == nil {
		t.Fatal("удалённого узла нет в корзине")
	}

	requireStatus(t, e.call(t, http.MethodDelete, "/api/trash", nil, nil), http.StatusOK)
	if list := e.trash(t); len(list) != 0 {
		t.Fatalf("корзина не пуста: %+v", list)
	}
	requireStatus(t, e.get(t, "/api/nodes/"+n.ID), http.StatusNotFound)
}

func TestExtensionSessions(t *testing.T) {
	e := newTestEnv(t)
	e.mustLogin(t)

	r := e.sendJSON(t, http.MethodPost, "/api/ext/login", map[string]any{
		"password": testPassword, "deviceId": "device-1",
	})
	requireStatus(t, r, http.StatusOK)

	list := e.get(t, "/api/ext/sessions")
	requireStatus(t, list, http.StatusOK)
	var sessions []store.ExtSession
	list.decode(t, &sessions)
	if len(sessions) != 1 || sessions[0].DeviceID != "device-1" {
		t.Fatalf("список сессий расширения: %+v", sessions)
	}

	requireStatus(t, e.call(t, http.MethodDelete, "/api/ext/sessions/"+sessions[0].ID, nil, nil), http.StatusOK)
	requireStatus(t, e.call(t, http.MethodDelete, "/api/ext/sessions/нет-такой", nil, nil), http.StatusNotFound)

	after := e.get(t, "/api/ext/sessions")
	requireStatus(t, after, http.StatusOK)
	if got := strings.TrimSpace(after.text()); got != "[]" {
		t.Fatalf("после удаления сессий тело %q, ожидался пустой массив", got)
	}
}

func TestImportBrowser(t *testing.T) {
	e := newTestEnv(t)
	e.mustLogin(t)

	r := e.sendJSON(t, http.MethodPost, "/api/import/browser", map[string]any{
		"folderName": "Из браузера",
		"tree": []any{
			map[string]any{"title": "Закладка", "url": "https://browser.example"},
			map[string]any{"title": "Вложенная папка", "children": []any{
				map[string]any{"title": "Глубже", "url": "https://deep.example"},
			}},
		},
	})
	requireStatus(t, r, http.StatusOK)
	var body struct {
		Created int `json:"created"`
		Known   int `json:"knownUrls"`
	}
	r.decode(t, &body)
	if body.Created != 4 {
		t.Fatalf("created = %d, ожидалось 4 (папка импорта, закладка, папка, вложенная закладка)", body.Created)
	}
	if body.Known != 0 {
		t.Fatalf("knownUrls = %d, ожидалось 0", body.Known)
	}

	var folder *store.Node
	for _, n := range e.tree(t) {
		if n.Title == "Из браузера" {
			folder = n
		}
	}
	if folder == nil {
		t.Fatal("папки импорта нет в дереве")
	}
	if len(folder.Children) != 2 {
		t.Fatalf("детей у папки импорта = %d, ожидалось 2", len(folder.Children))
	}
}

func TestChangePassword(t *testing.T) {
	e := newTestEnv(t)
	e.mustLogin(t)

	t.Run("неверный текущий пароль", func(t *testing.T) {
		r := e.sendJSON(t, http.MethodPost, "/api/password", map[string]any{
			"current": "не-тот-пароль", "next": "новый-пароль-теста",
		})
		requireStatus(t, r, http.StatusBadRequest)
		requireJSONError(t, r)
	})

	t.Run("смена пароля завершает веб-сессии", func(t *testing.T) {
		r := e.sendJSON(t, http.MethodPost, "/api/password", map[string]any{
			"current": testPassword, "next": "новый-пароль-теста",
		})
		requireStatus(t, r, http.StatusOK)
		var ok map[string]bool
		r.decode(t, &ok)
		if !ok["ok"] {
			t.Fatalf("ответ смены пароля: %s", r.Body)
		}

		requireStatus(t, e.get(t, "/api/tree"), http.StatusUnauthorized)
		requireRedirect(t, e.login(t, testPassword), http.StatusSeeOther, "/login?e=1")
		requireRedirect(t, e.login(t, "новый-пароль-теста"), http.StatusSeeOther, "/")
	})
}

func TestBackup(t *testing.T) {
	e := newTestEnv(t)
	e.mustLogin(t)
	e.createBookmark(t, "Резервируемая", "https://backup.example", nil)

	dir := t.TempDir()
	requireStatus(t, e.sendJSON(t, http.MethodPatch, "/api/settings", map[string]any{"backupDir": dir}), http.StatusOK)

	r := e.sendJSON(t, http.MethodPost, "/api/backup", nil)
	requireStatus(t, r, http.StatusOK)
	var body struct {
		JSON   string `json:"json"`
		SQLite string `json:"sqlite"`
	}
	r.decode(t, &body)
	for _, path := range []string{body.JSON, body.SQLite} {
		if !strings.HasPrefix(path, dir) {
			t.Fatalf("путь бэкапа %q вне каталога %q", path, dir)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("файла бэкапа %q нет: %v", path, err)
		}
	}

	settings := e.get(t, "/api/settings")
	requireStatus(t, settings, http.StatusOK)
	var got map[string]any
	settings.decode(t, &got)
	if got["lastBackupAt"] == "" {
		t.Fatalf("lastBackupAt не выставлен: %s", settings.Body)
	}
}
