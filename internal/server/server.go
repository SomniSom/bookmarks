package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"bookmarks/internal/auth"
	"bookmarks/internal/backup"
	"bookmarks/internal/config"
	"bookmarks/internal/links"
	"bookmarks/internal/og"
	"bookmarks/internal/store"
)

//go:embed templates/*.html static/*
var embedded embed.FS

const (
	cookieName = "bm_session"
	webTTL     = 30 * 24 * time.Hour
)

type identKind int

const (
	identWeb identKind = iota
	identExt
	identExtOpen
)

type identity struct {
	Kind identKind
}

type ctxKey int

const identKey ctxKey = 1

type Server struct {
	cfg      config.Config
	store    *store.Store
	links    *links.Checker
	secret   []byte
	tplLogin *template.Template
	tplApp   *template.Template
	tplTrash *template.Template
	tplSet   *template.Template
	limiter  *loginLimiter
	static   map[string]staticFile
}

func New(cfg config.Config, st *store.Store) (*Server, error) {
	sec := cfg.CookieSecret
	if sec == "" {
		v, err := st.CookieSecret(context.Background())
		if err != nil {
			return nil, err
		}
		sec = v
	}
	parse := func(name string) *template.Template {
		return template.Must(template.ParseFS(embedded, "templates/"+name))
	}
	staticFiles, err := loadStatic(embedded)
	if err != nil {
		return nil, err
	}
	s := &Server{
		cfg:      cfg,
		static:   staticFiles,
		store:    st,
		links:    &links.Checker{Store: st},
		secret:   []byte(sec),
		tplLogin: parse("login.html"),
		tplApp:   parse("app.html"),
		tplTrash: parse("trash.html"),
		tplSet:   parse("settings.html"),
		limiter:  newLoginLimiter(10, 5*time.Minute),
	}
	return s, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /static/{path...}", s.serveStatic)

	mux.HandleFunc("GET /login", s.pageLogin)
	mux.HandleFunc("POST /login", s.postLogin)
	mux.HandleFunc("POST /logout", s.webOnly(s.postLogout))

	mux.HandleFunc("GET /", s.webOnly(s.pageApp))
	mux.HandleFunc("GET /trash", s.webOnly(s.pageTrash))
	mux.HandleFunc("GET /settings", s.webOnly(s.pageSettings))

	mux.HandleFunc("POST /api/ext/login", s.apiExtLogin)

	mux.HandleFunc("GET /api/tree", s.apiAuth(s.apiTree, true))
	mux.HandleFunc("GET /api/search", s.apiAuth(s.apiSearch, true))
	mux.HandleFunc("GET /api/tags", s.apiAuth(s.apiTags, true))
	mux.HandleFunc("GET /api/duplicates", s.apiAuth(s.apiDuplicates, true))
	mux.HandleFunc("POST /api/nodes", s.apiAuth(s.apiCreateNode, true))
	mux.HandleFunc("POST /api/import/browser", s.apiAuth(s.apiImportBrowser, true))

	mux.HandleFunc("GET /api/nodes/{id}", s.webAPI(s.apiGetNode))
	mux.HandleFunc("PATCH /api/nodes/{id}", s.webAPI(s.apiPatchNode))
	mux.HandleFunc("DELETE /api/nodes/{id}", s.webAPI(s.apiDeleteNode))
	mux.HandleFunc("POST /api/nodes/{id}/move", s.webAPI(s.apiMoveNode))
	mux.HandleFunc("POST /api/nodes/{id}/restore", s.webAPI(s.apiRestore))
	mux.HandleFunc("DELETE /api/nodes/{id}/purge", s.webAPI(s.apiPurge))
	mux.HandleFunc("POST /api/nodes/{id}/og", s.webAPI(s.apiRefreshOG))
	mux.HandleFunc("POST /api/nodes/{id}/check-link", s.webAPI(s.apiCheckLink))
	mux.HandleFunc("GET /api/trash", s.webAPI(s.apiTrash))
	mux.HandleFunc("DELETE /api/trash", s.webAPI(s.apiEmptyTrash))
	mux.HandleFunc("POST /api/tags", s.webAPI(s.apiCreateTag))
	mux.HandleFunc("PATCH /api/tags/{id}", s.webAPI(s.apiPatchTag))
	mux.HandleFunc("DELETE /api/tags/{id}", s.webAPI(s.apiDeleteTag))
	mux.HandleFunc("GET /api/export", s.webAPI(s.apiExport))
	mux.HandleFunc("POST /api/import", s.webAPI(s.apiImportJSON))
	mux.HandleFunc("GET /api/settings", s.webAPI(s.apiGetSettings))
	mux.HandleFunc("PATCH /api/settings", s.webAPI(s.apiPatchSettings))
	mux.HandleFunc("POST /api/password", s.webAPI(s.apiPassword))
	mux.HandleFunc("POST /api/backup", s.webAPI(s.apiBackup))
	mux.HandleFunc("GET /api/ext/sessions", s.webAPI(s.apiExtSessions))
	mux.HandleFunc("DELETE /api/ext/sessions/{id}", s.webAPI(s.apiDeleteExtSession))
	mux.HandleFunc("POST /api/links/check-all", s.webAPI(s.apiCheckAll))
	mux.HandleFunc("GET /api/links/status", s.webAPI(s.apiLinkStatus))

	return s.securityHeaders(s.compress(s.cors(mux)))
}

func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if auth.IsExtensionOrigin(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Device-Id")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Max-Age", "600")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) signCookie(token string) string {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(token))
	return token + "." + hex.EncodeToString(mac.Sum(nil))
}

func (s *Server) parseCookie(v string) (string, bool) {
	token, sig, ok := strings.Cut(v, ".")
	if !ok || token == "" || sig == "" {
		return "", false
	}
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(token))
	want := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(sig), []byte(want)) {
		return "", false
	}
	return token, true
}

func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    s.signCookie(token),
		Path:     "/",
		MaxAge:   int(webTTL.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil || (s.cfg.TrustedProxy && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")),
	})
}

func (s *Server) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
}

func (s *Server) webIdentity(r *http.Request) bool {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return false
	}
	token, ok := s.parseCookie(c.Value)
	if !ok {
		return false
	}
	return s.store.TouchWebSession(r.Context(), token, webTTL)
}

func (s *Server) identifyAPI(r *http.Request) *identity {
	if s.webIdentity(r) {
		return &identity{Kind: identWeb}
	}
	origin := r.Header.Get("Origin")
	extOrigin := auth.IsExtensionOrigin(origin)
	hdr := r.Header.Get("Authorization")
	token := strings.TrimPrefix(hdr, "Bearer ")
	if token == hdr {
		token = ""
	}
	device := r.Header.Get("X-Device-Id")
	if token != "" {
		if _, err := s.store.GetExtByToken(r.Context(), token, device); err != nil {
			return nil
		}
		return &identity{Kind: identExt}
	}
	if extOrigin && !s.store.RequireExtAuth(r.Context()) {
		return &identity{Kind: identExtOpen}
	}
	return nil
}

func (s *Server) webOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.webIdentity(r) {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		next(w, r)
	}
}

func (s *Server) webAPI(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.webIdentity(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		ctx := context.WithValue(r.Context(), identKey, &identity{Kind: identWeb})
		next(w, r.WithContext(ctx))
	}
}

func (s *Server) apiAuth(next http.HandlerFunc, allowExt bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := s.identifyAPI(r)
		if id == nil || (!allowExt && id.Kind != identWeb) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), identKey, id)))
	}
}

// render собирает страницу в буфере: ошибка шаблона не должна отдавать
// клиенту половину HTML с кодом 200.
func (s *Server) render(w http.ResponseWriter, tpl *template.Template, data any) {
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		log.Printf("template %s: %v", tpl.Name(), err)
		http.Error(w, "ошибка рендера страницы", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(buf.Len()))
	_, _ = buf.WriteTo(w)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func readJSON(r *http.Request, v any) error {
	defer func() { _ = r.Body.Close() }()
	dec := json.NewDecoder(io.LimitReader(r.Body, 8<<20))
	return dec.Decode(v)
}

func (s *Server) pageLogin(w http.ResponseWriter, r *http.Request) {
	if s.webIdentity(r) {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	s.render(w, s.tplLogin, map[string]any{"Error": r.URL.Query().Get("e")})
}

func (s *Server) postLogin(w http.ResponseWriter, r *http.Request) {
	ip := auth.ClientIP(r, s.cfg.TrustedProxy)
	if !s.limiter.Allow(ip) {
		w.Header().Set("Retry-After", "300")
		http.Error(w, "слишком много попыток входа, попробуйте позже", http.StatusTooManyRequests)
		return
	}
	_ = r.ParseForm()
	pass := r.FormValue("password")
	if !s.store.CheckPassword(r.Context(), pass) {
		s.limiter.Fail(ip)
		http.Redirect(w, r, "/login?e=1", http.StatusSeeOther)
		return
	}
	s.limiter.Reset(ip)
	token, err := s.store.CreateWebSession(r.Context(), webTTL)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.setSessionCookie(w, r, token)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) postLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil {
		if token, ok := s.parseCookie(c.Value); ok {
			s.store.DeleteWebSession(r.Context(), token)
		}
	}
	s.clearCookie(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) pageApp(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	s.render(w, s.tplApp, nil)
}

func (s *Server) pageTrash(w http.ResponseWriter, _ *http.Request) {
	s.render(w, s.tplTrash, nil)
}

func (s *Server) pageSettings(w http.ResponseWriter, _ *http.Request) {
	s.render(w, s.tplSet, nil)
}

func (s *Server) apiExtLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
		DeviceID string `json:"deviceId"`
	}
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "bad json"})
		return
	}
	if strings.TrimSpace(req.DeviceID) == "" || !s.store.CheckPassword(r.Context(), req.Password) {
		writeJSON(w, 401, map[string]string{"error": "неверный пароль"})
		return
	}
	ua := r.Header.Get("User-Agent")
	token, sess, err := s.store.CreateExtSession(r.Context(), req.DeviceID, auth.ClientIP(r, s.cfg.TrustedProxy), ua, auth.BrowserFromUA(ua))
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"token": token, "session": sess})
}

func (s *Server) apiTree(w http.ResponseWriter, r *http.Request) {
	tree, err := s.store.Tree(r.Context())
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	if tree == nil {
		tree = []*store.Node{}
	}
	writeJSON(w, 200, tree)
}

func (s *Server) apiSearch(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.Search(r.Context(), r.URL.Query().Get("q"))
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	if list == nil {
		list = []*store.Node{}
	}
	writeJSON(w, 200, list)
}

func (s *Server) apiTags(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.Tags(r.Context())
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	if list == nil {
		list = []store.Tag{}
	}
	writeJSON(w, 200, list)
}

func (s *Server) apiDuplicates(w http.ResponseWriter, r *http.Request) {
	d, err := s.store.Duplicates(r.Context(), r.URL.Query().Get("url"), r.URL.Query().Get("except"))
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	if d == nil {
		d = []store.Duplicate{}
	}
	writeJSON(w, 200, d)
}

func (s *Server) apiCreateNode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Type             string   `json:"type"`
		ParentID         *string  `json:"parentId"`
		Title            string   `json:"title"`
		URL              string   `json:"url"`
		Notes            string   `json:"notes"`
		Tags             []string `json:"tags"`
		ConfirmDuplicate bool     `json:"confirmDuplicate"`
		FetchOG          *bool    `json:"fetchOG"`
	}
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "bad json"})
		return
	}
	if req.Type == "bookmark" && !req.ConfirmDuplicate {
		dups, _ := s.store.Duplicates(r.Context(), req.URL, "")
		if len(dups) > 0 {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "duplicate", "duplicates": dups})
			return
		}
	}
	n := &store.Node{Type: req.Type, ParentID: req.ParentID, Title: req.Title, URL: req.URL, Notes: req.Notes}
	created, err := s.store.CreateNode(r.Context(), n, req.Tags)
	if err != nil {
		code := 400
		if errors.Is(err, store.ErrForbiddenURL) {
			code = 400
		}
		writeJSON(w, code, map[string]string{"error": err.Error()})
		return
	}
	fetch := req.Type == "bookmark"
	if req.FetchOG != nil {
		fetch = *req.FetchOG
	}
	if fetch && created.Type == "bookmark" {
		s.applyOG(r.Context(), created)
		created, _ = s.store.GetNode(r.Context(), created.ID)
	}
	writeJSON(w, 201, created)
}

func (s *Server) applyOG(ctx context.Context, n *store.Node) {
	m := og.Fetch(ctx, n.URL)
	_ = s.store.UpdateOG(ctx, n.ID, m.Title, m.Description, m.Image, m.Favicon)
	if strings.TrimSpace(n.Title) == n.URL || strings.TrimSpace(n.Title) == "" {
		if m.Title != "" {
			t := m.Title
			_, _ = s.store.UpdateNode(ctx, n.ID, &t, nil, nil, nil)
		}
	}
}

func (s *Server) apiGetNode(w http.ResponseWriter, r *http.Request) {
	n, err := s.store.GetNode(r.Context(), r.PathValue("id"))
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "not found"})
		return
	}
	writeJSON(w, 200, n)
}

func (s *Server) apiPatchNode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title            *string         `json:"title"`
		URL              *string         `json:"url"`
		Notes            *string         `json:"notes"`
		Tags             *[]string       `json:"tags"`
		ParentID         json.RawMessage `json:"parentId"`
		ConfirmDuplicate bool            `json:"confirmDuplicate"`
	}
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "bad json"})
		return
	}
	id := r.PathValue("id")
	if req.URL != nil && !req.ConfirmDuplicate {
		dups, _ := s.store.Duplicates(r.Context(), *req.URL, id)
		if len(dups) > 0 {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "duplicate", "duplicates": dups})
			return
		}
	}
	old, _ := s.store.GetNode(r.Context(), id)
	n, err := s.store.UpdateNode(r.Context(), id, req.Title, req.URL, req.Notes, req.Tags)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	if len(req.ParentID) > 0 {
		var parent *string
		if string(req.ParentID) != "null" {
			var pid string
			if err := json.Unmarshal(req.ParentID, &pid); err != nil {
				writeJSON(w, 400, map[string]string{"error": "bad parentId"})
				return
			}
			if pid != "" {
				parent = &pid
			}
		}
		oldParent := ""
		if old != nil && old.ParentID != nil {
			oldParent = *old.ParentID
		}
		newParent := ""
		if parent != nil {
			newParent = *parent
		}
		if oldParent != newParent {
			if err := s.store.MoveNode(r.Context(), id, parent, nil); err != nil {
				msg := err.Error()
				if errors.Is(err, store.ErrCycle) {
					msg = "нельзя вложить папку в саму себя или в своего потомка"
				}
				if errors.Is(err, store.ErrInvalidParent) {
					msg = "недопустимая родительская папка"
				}
				writeJSON(w, 400, map[string]string{"error": msg})
				return
			}
			n, _ = s.store.GetNode(r.Context(), id)
		}
	}
	if req.URL != nil && old != nil && old.URL != n.URL {
		s.applyOG(r.Context(), n)
		n, _ = s.store.GetNode(r.Context(), n.ID)
	}
	writeJSON(w, 200, n)
}

func (s *Server) apiDeleteNode(w http.ResponseWriter, r *http.Request) {
	if err := s.store.SoftDelete(r.Context(), r.PathValue("id")); err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) apiMoveNode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ParentID *string `json:"parentId"`
		BeforeID *string `json:"beforeId"`
	}
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "bad json"})
		return
	}
	if err := s.store.MoveNode(r.Context(), r.PathValue("id"), req.ParentID, req.BeforeID); err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) apiRestore(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Restore(r.Context(), r.PathValue("id")); err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) apiPurge(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Purge(r.Context(), r.PathValue("id")); err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) apiRefreshOG(w http.ResponseWriter, r *http.Request) {
	n, err := s.store.GetNode(r.Context(), r.PathValue("id"))
	if err != nil || n.Type != "bookmark" {
		writeJSON(w, 404, map[string]string{"error": "not found"})
		return
	}
	s.applyOG(r.Context(), n)
	n, _ = s.store.GetNode(r.Context(), n.ID)
	writeJSON(w, 200, n)
}

func (s *Server) apiCheckLink(w http.ResponseWriter, r *http.Request) {
	n, err := s.store.GetNode(r.Context(), r.PathValue("id"))
	if err != nil || n.Type != "bookmark" {
		writeJSON(w, 404, map[string]string{"error": "not found"})
		return
	}
	st, msg := s.links.CheckOne(r.Context(), n.URL)
	var sp *int
	if st > 0 {
		sp = &st
	}
	_ = s.store.UpdateLink(r.Context(), n.ID, sp, msg)
	n, _ = s.store.GetNode(r.Context(), n.ID)
	writeJSON(w, 200, n)
}

func (s *Server) apiTrash(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.TrashRoots(r.Context())
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	if list == nil {
		list = []*store.Node{}
	}
	writeJSON(w, 200, list)
}

func (s *Server) apiEmptyTrash(w http.ResponseWriter, r *http.Request) {
	if err := s.store.EmptyTrash(r.Context()); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) apiCreateTag(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "bad json"})
		return
	}
	t, err := s.store.EnsureTag(r.Context(), req.Name)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 201, t)
}

func (s *Server) apiPatchTag(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "bad json"})
		return
	}
	if err := s.store.RenameTag(r.Context(), r.PathValue("id"), req.Name); err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) apiDeleteTag(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteTag(r.Context(), r.PathValue("id")); err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) apiExport(w http.ResponseWriter, r *http.Request) {
	include := r.URL.Query().Get("trash") == "1"
	data, err := s.store.ExportJSON(r.Context(), include)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", "attachment; filename=bookmarks.json")
	w.WriteHeader(200)
	_, _ = w.Write(data)
}

func (s *Server) apiImportJSON(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Replace bool               `json:"replace"`
		Nodes   []store.ImportNode `json:"nodes"`
	}
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "bad json"})
		return
	}
	if err := s.store.ImportJSON(r.Context(), req.Nodes, req.Replace); err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) apiImportBrowser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FolderName string              `json:"folderName"`
		Tree       []store.BrowserItem `json:"tree"`
	}
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "bad json"})
		return
	}
	if strings.TrimSpace(req.FolderName) == "" {
		req.FolderName = "Импорт браузера"
	}
	created, known, err := s.store.ImportBrowser(r.Context(), req.FolderName, req.Tree)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"created": created, "knownUrls": known})
}

func (s *Server) apiGetSettings(w http.ResponseWriter, r *http.Request) {
	reqAuth, _ := s.store.Setting(r.Context(), "require_extension_auth")
	dir, _ := s.store.Setting(r.Context(), "backup_dir")
	iv, _ := s.store.Setting(r.Context(), "backup_interval_hours")
	ret, _ := s.store.Setting(r.Context(), "backup_retention")
	last, _ := s.store.Setting(r.Context(), "last_backup_at")
	sessions, _ := s.store.ExtSessions(r.Context())
	if sessions == nil {
		sessions = []store.ExtSession{}
	}
	writeJSON(w, 200, map[string]any{
		"requireExtensionAuth": reqAuth != "0",
		"backupDir":            dir,
		"backupIntervalHours":  iv,
		"backupRetention":      ret,
		"lastBackupAt":         last,
		"listen":               s.cfg.Listen,
		"extensionSessions":    sessions,
	})
}

func (s *Server) apiPatchSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RequireExtensionAuth *bool   `json:"requireExtensionAuth"`
		BackupDir            *string `json:"backupDir"`
		BackupIntervalHours  *string `json:"backupIntervalHours"`
		BackupRetention      *string `json:"backupRetention"`
	}
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "bad json"})
		return
	}
	if req.RequireExtensionAuth != nil {
		v := "0"
		if *req.RequireExtensionAuth {
			v = "1"
		}
		_ = s.store.SetSetting(r.Context(), "require_extension_auth", v)
	}
	if req.BackupDir != nil {
		_ = s.store.SetSetting(r.Context(), "backup_dir", *req.BackupDir)
	}
	if req.BackupIntervalHours != nil {
		_ = s.store.SetSetting(r.Context(), "backup_interval_hours", *req.BackupIntervalHours)
	}
	if req.BackupRetention != nil {
		_ = s.store.SetSetting(r.Context(), "backup_retention", *req.BackupRetention)
	}
	s.apiGetSettings(w, r)
}

func (s *Server) apiPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Current string `json:"current"`
		Next    string `json:"next"`
	}
	if err := readJSON(r, &req); err != nil || strings.TrimSpace(req.Next) == "" {
		writeJSON(w, 400, map[string]string{"error": "укажите новый пароль"})
		return
	}
	if err := s.store.ChangePassword(r.Context(), req.Current, req.Next); err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	_ = s.store.DeleteAllWebSessions(r.Context())
	s.clearCookie(w)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) apiBackup(w http.ResponseWriter, r *http.Request) {
	j, q, err := backup.Run(r.Context(), s.store)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]string{"json": j, "sqlite": q})
}

func (s *Server) apiExtSessions(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ExtSessions(r.Context())
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	if list == nil {
		list = []store.ExtSession{}
	}
	writeJSON(w, 200, list)
}

func (s *Server) apiDeleteExtSession(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteExtSession(r.Context(), r.PathValue("id")); err != nil {
		writeJSON(w, 404, map[string]string{"error": "not found"})
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) apiCheckAll(w http.ResponseWriter, r *http.Request) {
	started := s.links.CheckAll(r.Context())
	writeJSON(w, 202, map[string]any{"started": started, "status": s.links.Status()})
}

func (s *Server) apiLinkStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, s.links.Status())
}
