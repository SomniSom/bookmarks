package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"bookmarks/internal/auth"

	// чистый Go-драйвер SQLite: бинарник собирается без cgo.
	_ "modernc.org/sqlite"
)

var (
	ErrNotFound      = errors.New("not found")
	ErrForbiddenURL  = errors.New("forbidden url")
	ErrCycle         = errors.New("cycle")
	ErrPasswordUnset = errors.New("password is not set; provide BOOKMARKS_PASSWORD on first run")
	ErrInvalidParent = errors.New("invalid parent")
)

type Store struct {
	db *sql.DB

	version atomic.Uint64
	tree    atomic.Pointer[treeSnapshot]
}

// treeSnapshot — готовое дерево для конкретной версии данных.
type treeSnapshot struct {
	version uint64
	roots   []*Node
}

// invalidate сбрасывает кэш дерева: версия меняется, следующий Tree соберёт его заново.
func (s *Store) invalidate() { s.version.Add(1) }

type Node struct {
	ID            string     `json:"id"`
	ParentID      *string    `json:"parentId"`
	Type          string     `json:"type"`
	Title         string     `json:"title"`
	URL           string     `json:"url,omitempty"`
	Notes         string     `json:"notes,omitempty"`
	SortOrder     int        `json:"sortOrder"`
	OGTitle       string     `json:"ogTitle,omitempty"`
	OGDescription string     `json:"ogDescription,omitempty"`
	OGImage       string     `json:"ogImage,omitempty"`
	Favicon       string     `json:"favicon,omitempty"`
	DeletedAt     *time.Time `json:"deletedAt,omitempty"`
	LinkStatus    *int       `json:"linkStatus,omitempty"`
	LinkCheckedAt *time.Time `json:"linkCheckedAt,omitempty"`
	LinkError     string     `json:"linkError,omitempty"`
	CreatedAt     time.Time  `json:"createdAt"`
	UpdatedAt     time.Time  `json:"updatedAt"`
	Tags          []Tag      `json:"tags,omitempty"`
	Children      []*Node    `json:"children,omitempty"`
	Path          string     `json:"path,omitempty"`
}

type Tag struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type WebSession struct {
	ID        string
	TokenHash string
	ExpiresAt time.Time
}

type ExtSession struct {
	ID         string    `json:"id"`
	DeviceID   string    `json:"deviceId"`
	IP         string    `json:"ip"`
	UserAgent  string    `json:"userAgent"`
	Browser    string    `json:"browser"`
	CreatedAt  time.Time `json:"createdAt"`
	LastSeenAt time.Time `json:"lastSeenAt"`
}

type Duplicate struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Path  string `json:"path"`
}

func Open(path string) (*Store, error) {
	ctx := context.Background()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil && filepath.Dir(path) != "." {
		return nil, err
	}
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)", filepath.ToSlash(path))
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS nodes (
  id TEXT PRIMARY KEY,
  parent_id TEXT REFERENCES nodes(id),
  type TEXT NOT NULL CHECK(type IN ('folder','bookmark')),
  title TEXT NOT NULL,
  url TEXT,
  notes TEXT,
  sort_order INTEGER NOT NULL DEFAULT 0,
  og_title TEXT,
  og_description TEXT,
  og_image TEXT,
  favicon TEXT,
  deleted_at TEXT,
  link_status INTEGER,
  link_checked_at TEXT,
  link_error TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_nodes_parent ON nodes(parent_id);
CREATE INDEX IF NOT EXISTS idx_nodes_deleted ON nodes(deleted_at);
CREATE INDEX IF NOT EXISTS idx_nodes_url ON nodes(url);

CREATE TABLE IF NOT EXISTS tags (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL COLLATE NOCASE UNIQUE
);

CREATE TABLE IF NOT EXISTS node_tags (
  node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
  tag_id TEXT NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
  PRIMARY KEY (node_id, tag_id)
);

CREATE TABLE IF NOT EXISTS web_sessions (
  id TEXT PRIMARY KEY,
  token_hash TEXT NOT NULL UNIQUE,
  expires_at TEXT NOT NULL,
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS extension_sessions (
  id TEXT PRIMARY KEY,
  token_hash TEXT NOT NULL UNIQUE,
  device_id TEXT NOT NULL,
  ip TEXT,
  user_agent TEXT,
  browser TEXT,
  created_at TEXT NOT NULL,
  last_seen_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
`)
	if err != nil {
		return err
	}
	defaults := map[string]string{
		"require_extension_auth": "1",
		"backup_dir":             "./backups",
		"backup_interval_hours":  "24",
		"backup_retention":       "14",
	}
	for k, v := range defaults {
		if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO settings(key, value) VALUES(?, ?)`, k, v); err != nil {
			return err
		}
	}
	return nil
}

func now() time.Time { return time.Now().UTC() }

func fmtTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t, _ = time.Parse(time.RFC3339, s)
	}
	return t
}

func parseTimePtr(ns sql.NullString) *time.Time {
	if !ns.Valid || ns.String == "" {
		return nil
	}
	t := parseTime(ns.String)
	return &t
}

func newID() string {
	tok, err := auth.RandomToken(16)
	if err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return tok
}

func (s *Store) Setting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO settings(key, value) VALUES(?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

func (s *Store) RequireExtAuth(ctx context.Context) bool {
	v, _ := s.Setting(ctx, "require_extension_auth")
	return v != "0" && !strings.EqualFold(v, "false")
}

func (s *Store) EnsurePassword(ctx context.Context, bootstrap string) error {
	hash, err := s.Setting(ctx, "password_hash")
	if err != nil {
		return err
	}
	if hash != "" {
		return nil
	}
	if strings.TrimSpace(bootstrap) == "" {
		return ErrPasswordUnset
	}
	encoded, err := auth.HashPassword(bootstrap)
	if err != nil {
		return err
	}
	return s.SetSetting(ctx, "password_hash", encoded)
}

func (s *Store) ChangePassword(ctx context.Context, current, next string) error {
	hash, err := s.Setting(ctx, "password_hash")
	if err != nil {
		return err
	}
	if hash == "" || !auth.VerifyPassword(hash, current) {
		return errors.New("неверный пароль")
	}
	encoded, err := auth.HashPassword(next)
	if err != nil {
		return err
	}
	return s.SetSetting(ctx, "password_hash", encoded)
}

func (s *Store) CheckPassword(ctx context.Context, password string) bool {
	hash, err := s.Setting(ctx, "password_hash")
	if err != nil || hash == "" {
		return false
	}
	return auth.VerifyPassword(hash, password)
}

func (s *Store) CookieSecret(ctx context.Context) (string, error) {
	v, err := s.Setting(ctx, "cookie_secret")
	if err != nil {
		return "", err
	}
	if v != "" {
		return v, nil
	}
	tok, err := auth.RandomToken(32)
	if err != nil {
		return "", err
	}
	if err := s.SetSetting(ctx, "cookie_secret", tok); err != nil {
		return "", err
	}
	return tok, nil
}

func IsForbiddenURL(raw string) bool {
	u := strings.ToLower(strings.TrimSpace(raw))
	if u == "" {
		return true
	}
	for _, p := range []string{"javascript:", "data:", "vbscript:"} {
		if strings.HasPrefix(u, p) || strings.Contains(u, "://"+p) {
			return true
		}
	}
	return false
}

func NormalizeURL(raw string) string {
	u := strings.TrimSpace(raw)
	if u == "" {
		return u
	}
	if !strings.Contains(u, "://") {
		u = "https://" + u
	}
	return u
}

func scanNode(scanner interface {
	Scan(dest ...any) error
}) (*Node, error) {
	var n Node
	var parent, url, notes, ogT, ogD, ogI, fav, del, lchk, lerr sql.NullString
	var lstatus sql.NullInt64
	var created, updated string
	if err := scanner.Scan(&n.ID, &parent, &n.Type, &n.Title, &url, &notes, &n.SortOrder,
		&ogT, &ogD, &ogI, &fav, &del, &lstatus, &lchk, &lerr, &created, &updated); err != nil {
		return nil, err
	}
	if parent.Valid {
		n.ParentID = &parent.String
	}
	n.URL = url.String
	n.Notes = notes.String
	n.OGTitle = ogT.String
	n.OGDescription = ogD.String
	n.OGImage = ogI.String
	n.Favicon = fav.String
	n.DeletedAt = parseTimePtr(del)
	if lstatus.Valid {
		v := int(lstatus.Int64)
		n.LinkStatus = &v
	}
	n.LinkCheckedAt = parseTimePtr(lchk)
	n.LinkError = lerr.String
	n.CreatedAt = parseTime(created)
	n.UpdatedAt = parseTime(updated)
	return &n, nil
}

const nodeCols = `id, parent_id, type, title, url, notes, sort_order, og_title, og_description, og_image, favicon, deleted_at, link_status, link_checked_at, link_error, created_at, updated_at`

func (s *Store) GetNode(ctx context.Context, id string) (*Node, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+nodeCols+` FROM nodes WHERE id = ?`, id)
	n, err := scanNode(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	tags, err := s.TagsForNode(ctx, id)
	if err != nil {
		return nil, err
	}
	n.Tags = tags
	n.Path = s.PathFor(ctx, id)
	return n, nil
}

func (s *Store) PathFor(ctx context.Context, id string) string {
	rows, err := s.db.QueryContext(ctx, `
WITH RECURSIVE chain(id, title, parent_id, depth) AS (
  SELECT id, title, parent_id, 0 FROM nodes WHERE id = ?
  UNION ALL
  SELECT n.id, n.title, n.parent_id, c.depth + 1
  FROM nodes n JOIN chain c ON n.id = c.parent_id
  WHERE c.depth < 64
)
SELECT title FROM chain ORDER BY depth DESC`, id)
	if err != nil {
		return ""
	}
	defer func() { _ = rows.Close() }()
	var parts []string
	for rows.Next() {
		var title string
		if err := rows.Scan(&title); err != nil {
			return strings.Join(parts, " / ")
		}
		parts = append(parts, title)
	}
	if err := rows.Err(); err != nil {
		return strings.Join(parts, " / ")
	}
	return strings.Join(parts, " / ")
}

func (s *Store) LiveNodes(ctx context.Context) ([]*Node, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+nodeCols+` FROM nodes WHERE deleted_at IS NULL ORDER BY sort_order, title COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var list []*Node
	byID := map[string]*Node{}
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		n.Tags = []Tag{}
		list = append(list, n)
		byID[n.ID] = n
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	_ = rows.Close()
	tagRows, err := s.db.QueryContext(ctx, `
SELECT nt.node_id, t.id, t.name
FROM node_tags nt JOIN tags t ON t.id = nt.tag_id
JOIN nodes n ON n.id = nt.node_id
WHERE n.deleted_at IS NULL`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tagRows.Close() }()
	for tagRows.Next() {
		var nid, tid, name string
		if err := tagRows.Scan(&nid, &tid, &name); err != nil {
			return nil, err
		}
		if n := byID[nid]; n != nil {
			n.Tags = append(n.Tags, Tag{ID: tid, Name: name})
		}
	}
	return list, tagRows.Err()
}

func (s *Store) Tree(ctx context.Context) ([]*Node, error) {
	version := s.version.Load()
	if snap := s.tree.Load(); snap != nil && snap.version == version {
		return snap.roots, nil
	}
	roots, err := s.buildTree(ctx)
	if err != nil {
		return nil, err
	}
	s.tree.Store(&treeSnapshot{version: version, roots: roots})
	return roots, nil
}

func (s *Store) buildTree(ctx context.Context) ([]*Node, error) {
	list, err := s.LiveNodes(ctx)
	if err != nil {
		return nil, err
	}
	byID := map[string]*Node{}
	for _, n := range list {
		n.Children = []*Node{}
		byID[n.ID] = n
	}
	var roots []*Node
	for _, n := range list {
		pid := n.ParentID
		if pid != nil && *pid != "" && *pid != n.ID && byID[*pid] != nil && !parentChainContains(*pid, n.ID, byID) {
			p := byID[*pid]
			p.Children = append(p.Children, n)
		} else {
			roots = append(roots, n)
		}
	}
	return roots, nil
}

func parentChainContains(startID, targetID string, byID map[string]*Node) bool {
	seen := map[string]struct{}{}
	cur := byID[startID]
	for i := 0; i < 128 && cur != nil; i++ {
		if cur.ID == targetID {
			return true
		}
		if _, ok := seen[cur.ID]; ok {
			return true
		}
		seen[cur.ID] = struct{}{}
		if cur.ParentID == nil || *cur.ParentID == "" {
			return false
		}
		cur = byID[*cur.ParentID]
	}
	return false
}

// execer — общий интерфейс *sql.DB и *sql.Tx: позволяет выполнять одни и те же
// вставки и в транзакции, и вне её.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func nextSort(ctx context.Context, e execer, parent *string) int {
	var maxOrder sql.NullInt64
	if parent == nil {
		_ = e.QueryRowContext(ctx, `SELECT MAX(sort_order) FROM nodes WHERE parent_id IS NULL AND deleted_at IS NULL`).Scan(&maxOrder)
	} else {
		_ = e.QueryRowContext(ctx, `SELECT MAX(sort_order) FROM nodes WHERE parent_id = ? AND deleted_at IS NULL`, *parent).Scan(&maxOrder)
	}
	if !maxOrder.Valid {
		return 0
	}
	return int(maxOrder.Int64) + 1
}

func insertNode(ctx context.Context, e execer, n *Node) error {
	_, err := e.ExecContext(ctx, `INSERT INTO nodes(`+nodeCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		n.ID, n.ParentID, n.Type, n.Title, nullIfEmpty(n.URL), nullIfEmpty(n.Notes), n.SortOrder,
		nullIfEmpty(n.OGTitle), nullIfEmpty(n.OGDescription), nullIfEmpty(n.OGImage), nullIfEmpty(n.Favicon),
		nil, n.LinkStatus, nil, nullIfEmpty(n.LinkError), fmtTime(n.CreatedAt), fmtTime(n.UpdatedAt))
	return err
}

func ensureTagID(ctx context.Context, e execer, name string) (string, error) {
	var t Tag
	err := e.QueryRowContext(ctx, `SELECT id, name FROM tags WHERE name = ? COLLATE NOCASE`, name).Scan(&t.ID, &t.Name)
	if err == nil {
		return t.ID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	t = Tag{ID: newID(), Name: name}
	_, err = e.ExecContext(ctx, `INSERT INTO tags(id, name) VALUES(?, ?)`, t.ID, t.Name)
	return t.ID, err
}

func setTags(ctx context.Context, e execer, nodeID string, names []string) error {
	if _, err := e.ExecContext(ctx, `DELETE FROM node_tags WHERE node_id=?`, nodeID); err != nil {
		return err
	}
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || seen[strings.ToLower(name)] {
			continue
		}
		seen[strings.ToLower(name)] = true
		tagID, err := ensureTagID(ctx, e, name)
		if err != nil {
			return err
		}
		if _, err := e.ExecContext(ctx, `INSERT OR IGNORE INTO node_tags(node_id, tag_id) VALUES(?, ?)`, nodeID, tagID); err != nil {
			return err
		}
	}
	return nil
}

// prepareNode fills in identifiers and timestamps for a new row.
func prepareNode(ctx context.Context, e execer, n *Node) {
	if n.ID == "" {
		n.ID = newID()
	}
	t := now()
	n.CreatedAt = t
	n.UpdatedAt = t
	n.SortOrder = nextSort(ctx, e, n.ParentID)
}

func (s *Store) CreateNode(ctx context.Context, n *Node, tagNames []string) (*Node, error) {
	s.invalidate()
	if n.Type != "folder" && n.Type != "bookmark" {
		return nil, errors.New("invalid type")
	}
	n.Title = strings.TrimSpace(n.Title)
	if n.Title == "" {
		return nil, errors.New("title required")
	}
	if n.Type == "bookmark" {
		if IsForbiddenURL(n.URL) {
			return nil, ErrForbiddenURL
		}
		n.URL = NormalizeURL(n.URL)
		if IsForbiddenURL(n.URL) {
			return nil, ErrForbiddenURL
		}
	} else {
		n.URL = ""
	}
	if n.ParentID != nil && *n.ParentID == "" {
		n.ParentID = nil
	}
	if n.ParentID != nil {
		p, err := s.GetNode(ctx, *n.ParentID)
		if err != nil {
			return nil, ErrInvalidParent
		}
		if p.DeletedAt != nil || p.Type != "folder" {
			return nil, ErrInvalidParent
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	prepareNode(ctx, tx, n)
	if err := insertNode(ctx, tx, n); err != nil {
		return nil, err
	}
	if n.Type == "bookmark" {
		if err := setTags(ctx, tx, n.ID, tagNames); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetNode(ctx, n.ID)
}

func nullIfEmpty(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

func (s *Store) UpdateNode(ctx context.Context, id string, title, url, notes *string, tagNames *[]string) (*Node, error) {
	s.invalidate()
	n, err := s.GetNode(ctx, id)
	if err != nil {
		return nil, err
	}
	if n.DeletedAt != nil {
		return nil, ErrNotFound
	}
	if title != nil {
		n.Title = strings.TrimSpace(*title)
		if n.Title == "" {
			return nil, errors.New("title required")
		}
	}
	if url != nil && n.Type == "bookmark" {
		if IsForbiddenURL(*url) {
			return nil, ErrForbiddenURL
		}
		n.URL = NormalizeURL(*url)
		if IsForbiddenURL(n.URL) {
			return nil, ErrForbiddenURL
		}
	}
	if notes != nil {
		n.Notes = *notes
	}
	n.UpdatedAt = now()
	_, err = s.db.ExecContext(ctx, `UPDATE nodes SET title=?, url=?, notes=?, updated_at=? WHERE id=?`,
		n.Title, nullIfEmpty(n.URL), nullIfEmpty(n.Notes), fmtTime(n.UpdatedAt), id)
	if err != nil {
		return nil, err
	}
	if tagNames != nil && n.Type == "bookmark" {
		if err := s.SetNodeTags(ctx, id, *tagNames); err != nil {
			return nil, err
		}
	}
	return s.GetNode(ctx, id)
}

func (s *Store) UpdateOG(ctx context.Context, id, title, desc, image, favicon string) error {
	s.invalidate()
	_, err := s.db.ExecContext(ctx, `UPDATE nodes SET og_title=?, og_description=?, og_image=?, favicon=?, updated_at=? WHERE id=?`,
		nullIfEmpty(title), nullIfEmpty(desc), nullIfEmpty(image), nullIfEmpty(favicon), fmtTime(now()), id)
	return err
}

func (s *Store) UpdateLink(ctx context.Context, id string, status *int, errMsg string) error {
	s.invalidate()
	var st any
	if status != nil {
		st = *status
	}
	var checked any
	if status != nil || errMsg != "" {
		checked = fmtTime(now())
	}
	_, err := s.db.ExecContext(ctx, `UPDATE nodes SET link_status=?, link_checked_at=?, link_error=?, updated_at=? WHERE id=?`,
		st, checked, nullIfEmpty(errMsg), fmtTime(now()), id)
	return err
}

func (s *Store) isDescendant(ctx context.Context, id, maybeAncestor string) bool {
	cur := id
	seen := map[string]bool{}
	for i := 0; i < 64 && cur != ""; i++ {
		if cur == maybeAncestor {
			return true
		}
		if seen[cur] {
			return true
		}
		seen[cur] = true
		var parent sql.NullString
		if err := s.db.QueryRowContext(ctx, `SELECT parent_id FROM nodes WHERE id=?`, cur).Scan(&parent); err != nil {
			return false
		}
		if !parent.Valid {
			return false
		}
		cur = parent.String
	}
	return false
}

func (s *Store) MoveNode(ctx context.Context, id string, parentID *string, beforeID *string) error {
	s.invalidate()
	n, err := s.GetNode(ctx, id)
	if err != nil {
		return err
	}
	if n.DeletedAt != nil {
		return ErrNotFound
	}
	if parentID != nil && *parentID == "" {
		parentID = nil
	}
	if parentID != nil {
		if *parentID == id || s.isDescendant(ctx, *parentID, id) {
			return ErrCycle
		}
		p, err := s.GetNode(ctx, *parentID)
		if err != nil || p.Type != "folder" || p.DeletedAt != nil {
			return ErrInvalidParent
		}
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE nodes SET parent_id=?, updated_at=? WHERE id=?`, parentID, fmtTime(now()), id); err != nil {
		return err
	}
	return s.reorderSiblings(ctx, parentID, id, beforeID)
}

func (s *Store) reorderSiblings(ctx context.Context, parentID *string, movedID string, beforeID *string) error {
	var rows *sql.Rows
	var err error
	if parentID == nil {
		rows, err = s.db.QueryContext(ctx, `SELECT id FROM nodes WHERE parent_id IS NULL AND deleted_at IS NULL AND id != ? ORDER BY sort_order, title COLLATE NOCASE`, movedID)
	} else {
		rows, err = s.db.QueryContext(ctx, `SELECT id FROM nodes WHERE parent_id = ? AND deleted_at IS NULL AND id != ? ORDER BY sort_order, title COLLATE NOCASE`, *parentID, movedID)
	}
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	inserted := false
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		if beforeID != nil && *beforeID == id && !inserted {
			ids = append(ids, movedID)
			inserted = true
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if !inserted {
		ids = append(ids, movedID)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.PrepareContext(ctx, `UPDATE nodes SET sort_order=? WHERE id=?`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()
	for i, id := range ids {
		if _, err := stmt.ExecContext(ctx, i, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) SoftDelete(ctx context.Context, id string) error {
	s.invalidate()
	t := fmtTime(now())
	_, err := s.db.ExecContext(ctx, `
WITH RECURSIVE tree(id) AS (
  SELECT id FROM nodes WHERE id = ?
  UNION
  SELECT n.id FROM nodes n JOIN tree t ON n.parent_id = t.id
)
UPDATE nodes SET deleted_at=?, updated_at=?
WHERE id IN (SELECT id FROM tree) AND deleted_at IS NULL`, id, t, t)
	return err
}

func (s *Store) Restore(ctx context.Context, id string) error {
	s.invalidate()
	n, err := s.GetNode(ctx, id)
	if err != nil {
		return err
	}
	if n.ParentID != nil {
		p, err := s.GetNode(ctx, *n.ParentID)
		if err != nil || p.DeletedAt != nil {
			_, err = s.db.ExecContext(ctx, `UPDATE nodes SET parent_id=NULL WHERE id=?`, id)
			if err != nil {
				return err
			}
		}
	}
	t := fmtTime(now())
	_, err = s.db.ExecContext(ctx, `
WITH RECURSIVE tree(id) AS (
  SELECT id FROM nodes WHERE id = ?
  UNION
  SELECT n.id FROM nodes n JOIN tree t ON n.parent_id = t.id
)
UPDATE nodes SET deleted_at=NULL, updated_at=? WHERE id IN (SELECT id FROM tree)`, id, t)
	return err
}

func (s *Store) Purge(ctx context.Context, id string) error {
	s.invalidate()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// Отложенная проверка FK: узлы удаляются одним запросом, порядок строк не определён.
	if _, err := tx.ExecContext(ctx, `PRAGMA defer_foreign_keys=ON`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
WITH RECURSIVE tree(id) AS (
  SELECT id FROM nodes WHERE id = ?
  UNION
  SELECT n.id FROM nodes n JOIN tree t ON n.parent_id = t.id
)
DELETE FROM node_tags WHERE node_id IN (SELECT id FROM tree)`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
WITH RECURSIVE tree(id) AS (
  SELECT id FROM nodes WHERE id = ?
  UNION
  SELECT n.id FROM nodes n JOIN tree t ON n.parent_id = t.id
)
DELETE FROM nodes WHERE id IN (SELECT id FROM tree)`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) EmptyTrash(ctx context.Context) error {
	s.invalidate()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `PRAGMA defer_foreign_keys=ON`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM node_tags WHERE node_id IN (SELECT id FROM nodes WHERE deleted_at IS NOT NULL)`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM nodes WHERE deleted_at IS NOT NULL`); err != nil {
		return err
	}
	// Теги, оставшиеся без узлов, больше не нужны.
	if _, err := tx.ExecContext(ctx, `DELETE FROM tags WHERE id NOT IN (SELECT tag_id FROM node_tags)`); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) TrashRoots(ctx context.Context) ([]*Node, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT n.id, n.parent_id, n.type, n.title, n.url, n.notes, n.sort_order, n.og_title, n.og_description, n.og_image, n.favicon, n.deleted_at, n.link_status, n.link_checked_at, n.link_error, n.created_at, n.updated_at
FROM nodes n
LEFT JOIN nodes p ON p.id = n.parent_id
WHERE n.deleted_at IS NOT NULL
AND (n.parent_id IS NULL OR p.deleted_at IS NULL)
ORDER BY n.deleted_at DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var list []*Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, n)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	_ = rows.Close()
	for _, n := range list {
		n.Path = s.PathFor(ctx, n.ID)
	}
	return list, nil
}

func (s *Store) Duplicates(ctx context.Context, url string, exceptID string) ([]Duplicate, error) {
	url = NormalizeURL(url)
	rows, err := s.db.QueryContext(ctx, `SELECT id, title FROM nodes WHERE type='bookmark' AND deleted_at IS NULL AND url = ? AND id != ?`, url, exceptID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Duplicate
	for rows.Next() {
		var d Duplicate
		if err := rows.Scan(&d.ID, &d.Title); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	_ = rows.Close()
	for i := range out {
		out[i].Path = s.PathFor(ctx, out[i].ID)
	}
	return out, nil
}

func (s *Store) Search(ctx context.Context, q string) ([]*Node, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return []*Node{}, nil
	}
	like := "%" + q + "%"
	rows, err := s.db.QueryContext(ctx, `
SELECT DISTINCT n.id, n.parent_id, n.type, n.title, n.url, n.notes, n.sort_order, n.og_title, n.og_description, n.og_image, n.favicon, n.deleted_at, n.link_status, n.link_checked_at, n.link_error, n.created_at, n.updated_at
FROM nodes n
LEFT JOIN node_tags nt ON nt.node_id = n.id
LEFT JOIN tags t ON t.id = nt.tag_id
WHERE n.deleted_at IS NULL AND n.type='bookmark'
AND (n.title LIKE ? COLLATE NOCASE OR IFNULL(n.url,'') LIKE ? COLLATE NOCASE OR IFNULL(t.name,'') LIKE ? COLLATE NOCASE)
ORDER BY n.title COLLATE NOCASE
LIMIT 50`, like, like, like)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var list []*Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, n)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	_ = rows.Close()
	for _, n := range list {
		n.Path = s.PathFor(ctx, n.ID)
	}
	return list, nil
}

func (s *Store) LiveBookmarks(ctx context.Context) ([]*Node, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+nodeCols+` FROM nodes WHERE type='bookmark' AND deleted_at IS NULL`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var list []*Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, n)
	}
	return list, rows.Err()
}

func (s *Store) Tags(ctx context.Context) ([]Tag, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name FROM tags ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var list []Tag
	for rows.Next() {
		var t Tag
		if err := rows.Scan(&t.ID, &t.Name); err != nil {
			return nil, err
		}
		list = append(list, t)
	}
	return list, rows.Err()
}

func (s *Store) TagsForNode(ctx context.Context, id string) ([]Tag, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT t.id, t.name FROM tags t JOIN node_tags nt ON nt.tag_id=t.id WHERE nt.node_id=? ORDER BY t.name COLLATE NOCASE`, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var list []Tag
	for rows.Next() {
		var t Tag
		if err := rows.Scan(&t.ID, &t.Name); err != nil {
			return nil, err
		}
		list = append(list, t)
	}
	if list == nil {
		list = []Tag{}
	}
	return list, rows.Err()
}

func (s *Store) EnsureTag(ctx context.Context, name string) (*Tag, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("empty tag")
	}
	var t Tag
	err := s.db.QueryRowContext(ctx, `SELECT id, name FROM tags WHERE name = ? COLLATE NOCASE`, name).Scan(&t.ID, &t.Name)
	if err == nil {
		return &t, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	t = Tag{ID: newID(), Name: name}
	_, err = s.db.ExecContext(ctx, `INSERT INTO tags(id, name) VALUES(?, ?)`, t.ID, t.Name)
	return &t, err
}

func (s *Store) RenameTag(ctx context.Context, id, name string) error {
	s.invalidate()
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("empty tag")
	}
	_, err := s.db.ExecContext(ctx, `UPDATE tags SET name=? WHERE id=?`, name, id)
	return err
}

func (s *Store) DeleteTag(ctx context.Context, id string) error {
	s.invalidate()
	if _, err := s.db.ExecContext(ctx, `DELETE FROM node_tags WHERE tag_id=?`, id); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM tags WHERE id=?`, id)
	return err
}

func (s *Store) SetNodeTags(ctx context.Context, nodeID string, names []string) error {
	s.invalidate()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := setTags(ctx, tx, nodeID, names); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) CreateWebSession(ctx context.Context, ttl time.Duration) (token string, err error) {
	token, err = auth.RandomToken(32)
	if err != nil {
		return "", err
	}
	id := newID()
	exp := now().Add(ttl)
	_, err = s.db.ExecContext(ctx, `INSERT INTO web_sessions(id, token_hash, expires_at, created_at) VALUES(?,?,?,?)`,
		id, auth.HashToken(token), fmtTime(exp), fmtTime(now()))
	return token, err
}

func (s *Store) TouchWebSession(ctx context.Context, token string, ttl time.Duration) bool {
	hash := auth.HashToken(token)
	var exp string
	err := s.db.QueryRowContext(ctx, `SELECT expires_at FROM web_sessions WHERE token_hash=?`, hash).Scan(&exp)
	if err != nil {
		return false
	}
	if parseTime(exp).Before(now()) {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM web_sessions WHERE token_hash=?`, hash)
		return false
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE web_sessions SET expires_at=? WHERE token_hash=?`, fmtTime(now().Add(ttl)), hash)
	return true
}

func (s *Store) DeleteWebSession(ctx context.Context, token string) {
	_, _ = s.db.ExecContext(ctx, `DELETE FROM web_sessions WHERE token_hash=?`, auth.HashToken(token))
}

func (s *Store) DeleteAllWebSessions(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM web_sessions`)
	return err
}

func (s *Store) CreateExtSession(ctx context.Context, deviceID, ip, ua, browser string) (token string, sess *ExtSession, err error) {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM extension_sessions WHERE device_id=?`, deviceID); err != nil {
		return "", nil, err
	}
	token, err = auth.RandomToken(32)
	if err != nil {
		return "", nil, err
	}
	sess = &ExtSession{
		ID:         newID(),
		DeviceID:   deviceID,
		IP:         ip,
		UserAgent:  ua,
		Browser:    browser,
		CreatedAt:  now(),
		LastSeenAt: now(),
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO extension_sessions(id, token_hash, device_id, ip, user_agent, browser, created_at, last_seen_at)
		VALUES(?,?,?,?,?,?,?,?)`, sess.ID, auth.HashToken(token), deviceID, ip, ua, browser, fmtTime(sess.CreatedAt), fmtTime(sess.LastSeenAt))
	return token, sess, err
}

func (s *Store) GetExtByToken(ctx context.Context, token, deviceID string) (*ExtSession, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, device_id, ip, user_agent, browser, created_at, last_seen_at, token_hash
		FROM extension_sessions WHERE token_hash=?`, auth.HashToken(token))
	var sess ExtSession
	var created, seen, hash string
	if err := row.Scan(&sess.ID, &sess.DeviceID, &sess.IP, &sess.UserAgent, &sess.Browser, &created, &seen, &hash); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if sess.DeviceID != deviceID {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM extension_sessions WHERE id=?`, sess.ID)
		return nil, ErrNotFound
	}
	sess.CreatedAt = parseTime(created)
	sess.LastSeenAt = parseTime(seen)
	_, _ = s.db.ExecContext(ctx, `UPDATE extension_sessions SET last_seen_at=? WHERE id=?`, fmtTime(now()), sess.ID)
	return &sess, nil
}

func (s *Store) ExtSessions(ctx context.Context) ([]ExtSession, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, device_id, ip, user_agent, browser, created_at, last_seen_at FROM extension_sessions ORDER BY last_seen_at DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var list []ExtSession
	for rows.Next() {
		var sss ExtSession
		var created, seen string
		if err := rows.Scan(&sss.ID, &sss.DeviceID, &sss.IP, &sss.UserAgent, &sss.Browser, &created, &seen); err != nil {
			return nil, err
		}
		sss.CreatedAt = parseTime(created)
		sss.LastSeenAt = parseTime(seen)
		list = append(list, sss)
	}
	return list, rows.Err()
}

func (s *Store) DeleteExtSession(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM extension_sessions WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

type ExportDoc struct {
	Nodes []*Node `json:"nodes"`
}

func (s *Store) ExportJSON(ctx context.Context, includeTrash bool) ([]byte, error) {
	var roots []*Node
	var err error
	if includeTrash {
		list, e := s.allNodesMap(ctx)
		if e != nil {
			return nil, e
		}
		roots = buildForest(list)
	} else {
		roots, err = s.Tree(ctx)
		if err != nil {
			return nil, err
		}
	}
	return json.MarshalIndent(ExportDoc{Nodes: roots}, "", "  ")
}

func (s *Store) allNodesMap(ctx context.Context) ([]*Node, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+nodeCols+` FROM nodes ORDER BY sort_order, title COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var list []*Node
	byID := map[string]*Node{}
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		n.Tags = []Tag{}
		list = append(list, n)
		byID[n.ID] = n
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	_ = rows.Close()
	tagRows, err := s.db.QueryContext(ctx, `SELECT nt.node_id, t.id, t.name FROM node_tags nt JOIN tags t ON t.id = nt.tag_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tagRows.Close() }()
	for tagRows.Next() {
		var nid, tid, name string
		if err := tagRows.Scan(&nid, &tid, &name); err != nil {
			return nil, err
		}
		if n := byID[nid]; n != nil {
			n.Tags = append(n.Tags, Tag{ID: tid, Name: name})
		}
	}
	return list, tagRows.Err()
}

func buildForest(list []*Node) []*Node {
	byID := map[string]*Node{}
	for _, n := range list {
		n.Children = []*Node{}
		byID[n.ID] = n
	}
	var roots []*Node
	for _, n := range list {
		if n.ParentID != nil && byID[*n.ParentID] != nil {
			byID[*n.ParentID].Children = append(byID[*n.ParentID].Children, n)
		} else {
			roots = append(roots, n)
		}
	}
	return roots
}

// TagNames принимает имена тегов и в коротком виде (["go","sql"]), и в виде
// объектов из собственного экспорта ([{"id":"...","name":"go"}]).
type TagNames []string

func (t *TagNames) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || string(data) == "null" {
		*t = nil
		return nil
	}
	var raws []json.RawMessage
	if err := json.Unmarshal(data, &raws); err != nil {
		return err
	}
	out := make(TagNames, 0, len(raws))
	for _, raw := range raws {
		var name string
		if err := json.Unmarshal(raw, &name); err == nil {
			out = append(out, name)
			continue
		}
		var obj struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &obj); err != nil {
			return fmt.Errorf("tag: %w", err)
		}
		out = append(out, obj.Name)
	}
	*t = out
	return nil
}

type ImportNode struct {
	Type     string       `json:"type"`
	Title    string       `json:"title"`
	URL      string       `json:"url"`
	Notes    string       `json:"notes"`
	Tags     TagNames     `json:"tags"`
	Children []ImportNode `json:"children"`
}

func (s *Store) ImportJSON(ctx context.Context, doc []ImportNode, replace bool) error {
	s.invalidate()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if replace {
		if _, err := tx.ExecContext(ctx, `DELETE FROM node_tags`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM nodes`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM tags`); err != nil {
			return err
		}
	}
	for _, n := range doc {
		if err := importOne(ctx, tx, nil, n); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func importOne(ctx context.Context, e execer, parent *string, in ImportNode) error {
	typ := in.Type
	if typ == "" {
		if in.URL != "" {
			typ = "bookmark"
		} else {
			typ = "folder"
		}
	}
	title := strings.TrimSpace(in.Title)
	n := &Node{Type: typ, Title: title, URL: in.URL, Notes: in.Notes, ParentID: parent}
	var tags []string
	if typ == "bookmark" {
		tags = []string(in.Tags)
		n.URL = NormalizeURL(n.URL)
		if IsForbiddenURL(n.URL) {
			return nil
		}
		if n.Title == "" {
			n.Title = n.URL
		}
	} else {
		n.URL = ""
		if n.Title == "" {
			n.Title = "Папка"
		}
	}
	prepareNode(ctx, e, n)
	if err := insertNode(ctx, e, n); err != nil {
		return err
	}
	if n.Type == "bookmark" {
		if err := setTags(ctx, e, n.ID, tags); err != nil {
			return err
		}
	}
	for _, ch := range in.Children {
		pid := n.ID
		if err := importOne(ctx, e, &pid, ch); err != nil {
			return err
		}
	}
	return nil
}

type BrowserItem struct {
	Title    string        `json:"title"`
	URL      string        `json:"url"`
	Children []BrowserItem `json:"children"`
}

func (s *Store) ImportBrowser(ctx context.Context, folderName string, tree []BrowserItem) (created int, known int, err error) {
	s.invalidate()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = tx.Rollback() }()

	root := &Node{Type: "folder", Title: strings.TrimSpace(folderName)}
	if root.Title == "" {
		root.Title = "Импорт браузера"
	}
	prepareNode(ctx, tx, root)
	if err := insertNode(ctx, tx, root); err != nil {
		return 0, 0, err
	}
	created = 1

	var walk func(parent string, items []BrowserItem) error
	walk = func(parent string, items []BrowserItem) error {
		for _, it := range items {
			title := strings.TrimSpace(it.Title)
			if it.URL != "" {
				u := NormalizeURL(it.URL)
				if IsForbiddenURL(u) {
					continue
				}
				var n int
				if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM nodes WHERE type='bookmark' AND deleted_at IS NULL AND url = ?`, u).Scan(&n); err != nil {
					return err
				}
				known += n
				if title == "" {
					title = u
				}
				pid := parent
				b := &Node{Type: "bookmark", Title: title, URL: u, ParentID: &pid}
				prepareNode(ctx, tx, b)
				if err := insertNode(ctx, tx, b); err != nil {
					return err
				}
				created++
				continue
			}
			if title == "" {
				title = "Папка"
			}
			pid := parent
			f := &Node{Type: "folder", Title: title, ParentID: &pid}
			prepareNode(ctx, tx, f)
			if err := insertNode(ctx, tx, f); err != nil {
				return err
			}
			created++
			if err := walk(f.ID, it.Children); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(root.ID, tree); err != nil {
		return 0, 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	return created, known, nil
}

func (s *Store) VacuumInto(ctx context.Context, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil && filepath.Dir(path) != "." {
		return err
	}
	_, err := s.db.ExecContext(ctx, `VACUUM INTO ?`, path)
	return err
}
