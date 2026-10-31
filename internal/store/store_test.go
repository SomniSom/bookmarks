package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

var testCtx = context.Background()

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir() + "/test.sqlite")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func mustFolder(t *testing.T, s *Store, title string, parent *string) *Node {
	t.Helper()
	n, err := s.CreateNode(testCtx, &Node{Type: "folder", Title: title, ParentID: parent}, nil)
	if err != nil {
		t.Fatalf("CreateNode folder %q: %v", title, err)
	}
	return n
}

func mustBookmark(t *testing.T, s *Store, title, url string, parent *string, tags []string) *Node {
	t.Helper()
	n, err := s.CreateNode(testCtx, &Node{Type: "bookmark", Title: title, URL: url, ParentID: parent}, tags)
	if err != nil {
		t.Fatalf("CreateNode bookmark %q: %v", title, err)
	}
	return n
}

func TestCreateAndTree(t *testing.T) {
	s := newTestStore(t)
	root := mustFolder(t, s, "Корень", nil)
	child := mustFolder(t, s, "Дочерняя", &root.ID)
	mustBookmark(t, s, "Пример", "example.com", &child.ID, []string{"Go", "тест"})

	tree, err := s.Tree(testCtx)
	if err != nil {
		t.Fatalf("Tree: %v", err)
	}
	if len(tree) != 1 {
		t.Fatalf("корней = %d, ожидался 1", len(tree))
	}
	if tree[0].ID != root.ID {
		t.Fatalf("корень %q, ожидался %q", tree[0].ID, root.ID)
	}
	if len(tree[0].Children) != 1 || tree[0].Children[0].ID != child.ID {
		t.Fatalf("ожидался один дочерний узел %q", child.ID)
	}
	grand := tree[0].Children[0].Children
	if len(grand) != 1 || grand[0].URL != "https://example.com" {
		t.Fatalf("закладка не нормализована: %+v", grand)
	}
	if len(grand[0].Tags) != 2 {
		t.Fatalf("тегов = %d, ожидалось 2", len(grand[0].Tags))
	}
}

func TestCreateNodeValidation(t *testing.T) {
	s := newTestStore(t)
	tests := []struct {
		name string
		node *Node
	}{
		{"пустой тип", &Node{Title: "x", Type: "note"}},
		{"пустой заголовок", &Node{Title: "   ", Type: "folder"}},
		{"javascript", &Node{Title: "x", Type: "bookmark", URL: "javascript:alert(1)"}},
		{"data url", &Node{Title: "x", Type: "bookmark", URL: "data:text/html,hi"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.CreateNode(testCtx, tc.node, nil); err == nil {
				t.Fatal("ожидалась ошибка")
			}
		})
	}
	folder := mustFolder(t, s, "Папка", nil)
	late := mustFolder(t, s, "Удалённая", nil)
	if err := s.SoftDelete(testCtx, late.ID); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}
	if _, err := s.CreateNode(testCtx, &Node{Type: "bookmark", Title: "x", URL: "https://ok.example", ParentID: &late.ID}, nil); !errors.Is(err, ErrInvalidParent) {
		t.Fatalf("вложении в удалённую папку: %v", err)
	}
	if _, err := s.CreateNode(testCtx, &Node{Type: "bookmark", Title: "x", URL: "https://ok.example", ParentID: &folder.ID}, nil); err != nil {
		t.Fatalf("вложение в живую папку: %v", err)
	}
}

func TestMoveNodeCycle(t *testing.T) {
	s := newTestStore(t)
	a := mustFolder(t, s, "A", nil)
	b := mustFolder(t, s, "B", &a.ID)
	c := mustFolder(t, s, "C", &b.ID)

	if err := s.MoveNode(testCtx, a.ID, &c.ID, nil); !errors.Is(err, ErrCycle) {
		t.Fatalf("ожидался ErrCycle, получено %v", err)
	}
	if err := s.MoveNode(testCtx, c.ID, nil, nil); err != nil {
		t.Fatalf("перенос в корень: %v", err)
	}
	n, err := s.GetNode(testCtx, c.ID)
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if n.ParentID != nil {
		t.Fatalf("parentId = %v, ожидался nil", *n.ParentID)
	}
}

func TestSoftDeleteRestorePurge(t *testing.T) {
	s := newTestStore(t)
	root := mustFolder(t, s, "Корень", nil)
	child := mustBookmark(t, s, "Ссылка", "https://example.com/a", &root.ID, nil)

	if err := s.SoftDelete(testCtx, root.ID); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}
	live, err := s.Tree(testCtx)
	if err != nil {
		t.Fatalf("Tree: %v", err)
	}
	if len(live) != 0 {
		t.Fatalf("после удаления видно %d узлов", len(live))
	}
	roots, err := s.TrashRoots(testCtx)
	if err != nil {
		t.Fatalf("TrashRoots: %v", err)
	}
	if len(roots) != 1 || roots[0].ID != root.ID {
		t.Fatalf("корней корзины = %d, ожидался 1 (%q)", len(roots), root.ID)
	}
	if roots[0].Path != "Корень" {
		t.Fatalf("путь = %q", roots[0].Path)
	}

	if err := s.Restore(testCtx, root.ID); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if n, err := s.GetNode(testCtx, child.ID); err != nil || n.DeletedAt != nil {
		t.Fatalf("дочерний узел не восстановлен: %v", err)
	}

	if err := s.SoftDelete(testCtx, root.ID); err != nil {
		t.Fatalf("SoftDelete 2: %v", err)
	}
	if err := s.Purge(testCtx, root.ID); err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if _, err := s.GetNode(testCtx, child.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("после Purge ожидался ErrNotFound, получено %v", err)
	}
}

func TestDuplicatesAndSearch(t *testing.T) {
	s := newTestStore(t)
	f := mustFolder(t, s, "Папка", nil)
	first := mustBookmark(t, s, "Первый", "https://example.com/page", &f.ID, []string{"работа"})
	found, err := s.Duplicates(testCtx, "example.com/page", "")
	if err != nil {
		t.Fatalf("Duplicates: %v", err)
	}
	if len(found) != 1 || found[0].ID != first.ID {
		t.Fatalf("дубликаты = %+v", found)
	}
	if _, err := s.Duplicates(testCtx, "example.com/page", first.ID); err != nil {
		t.Fatalf("Duplicates except: %v", err)
	}
	res, err := s.Search(testCtx, "работ")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res) != 1 || res[0].Path != "Папка / Первый" {
		t.Fatalf("поиск по тегу: %+v", res)
	}
	if _, err := s.Search(testCtx, "   "); err != nil {
		t.Fatalf("пустой поиск: %v", err)
	}
}

func TestTagLifecycle(t *testing.T) {
	s := newTestStore(t)
	n := mustBookmark(t, s, "Ссылка", "https://example.com", nil, []string{"One", "one ", "Two"})
	stored, err := s.TagsForNode(testCtx, n.ID)
	if err != nil {
		t.Fatalf("TagsForNode: %v", err)
	}
	if len(stored) != 2 {
		t.Fatalf("тегов = %d, ожидалось 2 (дубли без учёта регистра)", len(stored))
	}
	if err := s.RenameTag(testCtx, stored[0].ID, "Переименован"); err != nil {
		t.Fatalf("RenameTag: %v", err)
	}
	if err := s.DeleteTag(testCtx, stored[0].ID); err != nil {
		t.Fatalf("DeleteTag: %v", err)
	}
	left, err := s.TagsForNode(testCtx, n.ID)
	if err != nil {
		t.Fatalf("TagsForNode 2: %v", err)
	}
	if len(left) != 1 {
		t.Fatalf("после удаления тегов = %d", len(left))
	}
	if _, err := s.EnsureTag(testCtx, "  "); err == nil {
		t.Fatal("пустой тег должен давать ошибку")
	}
}

func TestExportImport(t *testing.T) {
	s := newTestStore(t)
	f := mustFolder(t, s, "Папка", nil)
	mustBookmark(t, s, "Ссылка", "https://example.com/x", &f.ID, []string{"тег"})

	data, err := s.ExportJSON(testCtx, false)
	if err != nil {
		t.Fatalf("ExportJSON: %v", err)
	}
	var doc ExportDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("json: %v", err)
	}
	if len(doc.Nodes) != 1 || doc.Nodes[0].Children == nil {
		t.Fatalf("экспорт: %+v", doc.Nodes)
	}

	var payload struct {
		Nodes []ImportNode `json:"nodes"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("json import: %v", err)
	}

	s2 := newTestStore(t)
	if err := s2.ImportJSON(testCtx, payload.Nodes, false); err != nil {
		t.Fatalf("ImportJSON: %v", err)
	}
	tree, err := s2.Tree(testCtx)
	if err != nil {
		t.Fatalf("Tree: %v", err)
	}
	if len(tree) != 1 || len(tree[0].Children) != 1 {
		t.Fatalf("импорт не восстановил дерево: %+v", tree)
	}
}

func TestImportBrowserSkipsForbidden(t *testing.T) {
	s := newTestStore(t)
	created, known, err := s.ImportBrowser(testCtx, "Из браузера", []BrowserItem{
		{Title: "Папка", Children: []BrowserItem{
			{Title: "Хорошая", URL: "https://example.com"},
			{Title: "Плохая", URL: "javascript:alert(1)"},
			{Title: "", URL: "https://second.example"},
		}},
	})
	if err != nil {
		t.Fatalf("ImportBrowser: %v", err)
	}
	if created != 4 {
		t.Fatalf("создано = %d, ожидалось 4", created)
	}
	if known != 0 {
		t.Fatalf("известных = %d", known)
	}
	list, err := s.LiveBookmarks(testCtx)
	if err != nil {
		t.Fatalf("LiveBookmarks: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("закладок = %d, ожидалось 2", len(list))
	}
}

func TestWebAndExtSessions(t *testing.T) {
	s := newTestStore(t)
	token, err := s.CreateWebSession(testCtx, 3600e9)
	if err != nil {
		t.Fatalf("CreateWebSession: %v", err)
	}
	if !s.TouchWebSession(testCtx, token, 3600e9) {
		t.Fatal("сессия должна быть живой")
	}
	s.DeleteWebSession(testCtx, token)
	if s.TouchWebSession(testCtx, token, 3600e9) {
		t.Fatal("после удаления сессия не должна проходить")
	}
	if err := s.DeleteAllWebSessions(testCtx); err != nil {
		t.Fatalf("DeleteAllWebSessions: %v", err)
	}

	extToken, sess, err := s.CreateExtSession(testCtx, "device-1", "127.0.0.1", "UA", "Chrome 1")
	if err != nil {
		t.Fatalf("CreateExtSession: %v", err)
	}
	if _, err := s.GetExtByToken(testCtx, extToken, "device-1"); err != nil {
		t.Fatalf("GetExtByToken: %v", err)
	}
	if _, err := s.GetExtByToken(testCtx, extToken, "device-2"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("чужое устройство: %v", err)
	}
	list, err := s.ExtSessions(testCtx)
	if err != nil {
		t.Fatalf("ExtSessions: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("сессия чужого устройства должна удаляться, осталось %d", len(list))
	}
	if err := s.DeleteExtSession(testCtx, sess.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("удаление несуществующей сессии: %v", err)
	}
}

func TestPasswordFlow(t *testing.T) {
	s := newTestStore(t)
	if err := s.EnsurePassword(testCtx, ""); !errors.Is(err, ErrPasswordUnset) {
		t.Fatalf("без пароля: %v", err)
	}
	if err := s.EnsurePassword(testCtx, "секрет"); err != nil {
		t.Fatalf("EnsurePassword: %v", err)
	}
	if !s.CheckPassword(testCtx, "секрет") {
		t.Fatal("пароль не подошёл")
	}
	if s.CheckPassword(testCtx, "другой") {
		t.Fatal("чужой пароль подошёл")
	}
	if err := s.ChangePassword(testCtx, "неверный", "новый"); err == nil {
		t.Fatal("смена с неверным текущим паролем должна падать")
	}
	if err := s.ChangePassword(testCtx, "секрет", "новый"); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	if !s.CheckPassword(testCtx, "новый") {
		t.Fatal("новый пароль не подошёл")
	}
}

func TestSettingsAndCookieSecret(t *testing.T) {
	s := newTestStore(t)
	if s.RequireExtAuth(testCtx) != true {
		t.Fatal("по умолчанию require_extension_auth включён")
	}
	if err := s.SetSetting(testCtx, "require_extension_auth", "0"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	if s.RequireExtAuth(testCtx) {
		t.Fatal("после 0 аутентификация расширений должна быть выключена")
	}
	secret, err := s.CookieSecret(testCtx)
	if err != nil {
		t.Fatalf("CookieSecret: %v", err)
	}
	again, err := s.CookieSecret(testCtx)
	if err != nil || again != secret {
		t.Fatalf("секрет не сохраняется: %q vs %q", secret, again)
	}
	if v, err := s.Setting(testCtx, "нет-такого"); err != nil || v != "" {
		t.Fatalf("неизвестный ключ: %q %v", v, err)
	}
}

func TestVacuumInto(t *testing.T) {
	s := newTestStore(t)
	mustFolder(t, s, "Папка", nil)
	dir := t.TempDir()
	path := dir + "/backup.sqlite"
	if err := s.VacuumInto(testCtx, path); err != nil {
		t.Fatalf("VacuumInto: %v", err)
	}
	copyStore, err := Open(path)
	if err != nil {
		t.Fatalf("открытие копии: %v", err)
	}
	defer func() { _ = copyStore.Close() }()
	tree, err := copyStore.Tree(testCtx)
	if err != nil {
		t.Fatalf("Tree копии: %v", err)
	}
	if len(tree) != 1 {
		t.Fatalf("узлов в копии = %d", len(tree))
	}
}

func TestNormalizeAndForbiddenURL(t *testing.T) {
	if got := NormalizeURL("  example.com  "); got != "https://example.com" {
		t.Fatalf("NormalizeURL = %q", got)
	}
	if got := NormalizeURL("http://example.com"); got != "http://example.com" {
		t.Fatalf("NormalizeURL схема = %q", got)
	}
	for _, raw := range []string{"", "   ", "javascript:alert(1)", "VBScript:x", "data:text/html,x"} {
		if !IsForbiddenURL(raw) {
			t.Fatalf("%q должен быть запрещён", raw)
		}
	}
	if IsForbiddenURL("https://example.com") {
		t.Fatal("обычный URL запрещён")
	}
}

func TestPathForBrokenParent(t *testing.T) {
	s := newTestStore(t)
	f := mustFolder(t, s, "Папка", nil)
	b := mustBookmark(t, s, "Ссылка", "https://example.com", &f.ID, nil)
	if got := s.PathFor(testCtx, b.ID); got != "Папка / Ссылка" {
		t.Fatalf("PathFor = %q", got)
	}
	if got := s.PathFor(testCtx, "нет-такого"); got != "" {
		t.Fatalf("PathFor несуществующего = %q", got)
	}
}

func TestEmptyTrash(t *testing.T) {
	s := newTestStore(t)
	f := mustFolder(t, s, "Папка", nil)
	mustBookmark(t, s, "Ссылка", "https://example.com", &f.ID, []string{"x"})
	if err := s.SoftDelete(testCtx, f.ID); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}
	if err := s.EmptyTrash(testCtx); err != nil {
		t.Fatalf("EmptyTrash: %v", err)
	}
	rows, err := s.DB().Query(`SELECT COUNT(*) FROM nodes`)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	var n int
	if !rows.Next() {
		_ = rows.Close()
		t.Fatal("нет строки с COUNT(*)")
	}
	if err := rows.Scan(&n); err != nil {
		_ = rows.Close()
		t.Fatalf("scan: %v", err)
	}
	_ = rows.Close()
	if n != 0 {
		t.Fatalf("после EmptyTrash осталось %d узлов", n)
	}
	if _, err := s.Tags(testCtx); err != nil {
		t.Fatalf("Tags: %v", err)
	}
}

func TestNodeIDUniquenessAndTitleTrim(t *testing.T) {
	s := newTestStore(t)
	a := mustFolder(t, s, "  Пробелы  ", nil)
	if a.Title != "Пробелы" {
		t.Fatalf("заголовок = %q", a.Title)
	}
	b := mustFolder(t, s, "Второй", nil)
	if a.ID == b.ID {
		t.Fatal("идентификаторы совпали")
	}
	if !strings.HasPrefix(a.CreatedAt.String(), "2026") && a.CreatedAt.IsZero() {
		t.Fatalf("createdAt = %v", a.CreatedAt)
	}
}

func TestUpdateNode(t *testing.T) {
	s := newTestStore(t)
	f := mustFolder(t, s, "Папка", nil)
	b := mustBookmark(t, s, "Старое", "https://old.example", &f.ID, []string{"было"})

	title, url, notes := "Новое", "new.example", "заметка"
	tags := []string{"стало", "ещё"}
	n, err := s.UpdateNode(testCtx, b.ID, &title, &url, &notes, &tags)
	if err != nil {
		t.Fatalf("UpdateNode: %v", err)
	}
	if n.Title != "Новое" || n.URL != "https://new.example" || n.Notes != "заметка" {
		t.Fatalf("поля не обновились: %+v", n)
	}
	if len(n.Tags) != 2 {
		t.Fatalf("тегов = %d, ожидалось 2", len(n.Tags))
	}

	empty := "   "
	if _, err := s.UpdateNode(testCtx, b.ID, &empty, nil, nil, nil); err == nil {
		t.Fatal("пустой заголовок должен давать ошибку")
	}
	bad := "javascript:alert(1)"
	if _, err := s.UpdateNode(testCtx, b.ID, nil, &bad, nil, nil); !errors.Is(err, ErrForbiddenURL) {
		t.Fatalf("запрещённый URL: %v", err)
	}
	if err := s.SoftDelete(testCtx, f.ID); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}
	if _, err := s.UpdateNode(testCtx, b.ID, &title, nil, nil, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("удалённый узел: %v", err)
	}
}

func TestUpdateOGAndLink(t *testing.T) {
	s := newTestStore(t)
	b := mustBookmark(t, s, "Ссылка", "https://example.com", nil, nil)

	if err := s.UpdateOG(testCtx, b.ID, "Заголовок", "Описание", "https://example.com/i.png", "https://example.com/f.ico"); err != nil {
		t.Fatalf("UpdateOG: %v", err)
	}
	n, err := s.GetNode(testCtx, b.ID)
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if n.OGTitle != "Заголовок" || n.OGDescription != "Описание" || n.OGImage == "" || n.Favicon == "" {
		t.Fatalf("OG не сохранился: %+v", n)
	}

	status := 200
	if err := s.UpdateLink(testCtx, b.ID, &status, ""); err != nil {
		t.Fatalf("UpdateLink: %v", err)
	}
	n, _ = s.GetNode(testCtx, b.ID)
	if n.LinkStatus == nil || *n.LinkStatus != 200 || n.LinkCheckedAt == nil {
		t.Fatalf("статус ссылки не сохранился: %+v", n)
	}

	if err := s.UpdateLink(testCtx, b.ID, nil, "connection refused"); err != nil {
		t.Fatalf("UpdateLink с ошибкой: %v", err)
	}
	n, _ = s.GetNode(testCtx, b.ID)
	if n.LinkStatus != nil {
		t.Fatalf("статус должен быть пустым, получен %d", *n.LinkStatus)
	}
	if n.LinkError != "connection refused" || n.LinkCheckedAt == nil {
		t.Fatalf("ошибка ссылки не сохранилась: %+v", n)
	}
}

func TestSetNodeTags(t *testing.T) {
	s := newTestStore(t)
	b := mustBookmark(t, s, "Ссылка", "https://example.com", nil, []string{"a", "b"})

	if err := s.SetNodeTags(testCtx, b.ID, []string{"c", "  ", "C", "d"}); err != nil {
		t.Fatalf("SetNodeTags: %v", err)
	}
	tags, err := s.TagsForNode(testCtx, b.ID)
	if err != nil {
		t.Fatalf("TagsForNode: %v", err)
	}
	if len(tags) != 2 {
		t.Fatalf("тегов = %d, ожидалось 2 (c и d)", len(tags))
	}
	if err := s.SetNodeTags(testCtx, b.ID, nil); err != nil {
		t.Fatalf("очистка тегов: %v", err)
	}
	tags, _ = s.TagsForNode(testCtx, b.ID)
	if len(tags) != 0 {
		t.Fatalf("после очистки тегов = %d", len(tags))
	}
}

func TestExportWithTrash(t *testing.T) {
	s := newTestStore(t)
	f := mustFolder(t, s, "Папка", nil)
	mustBookmark(t, s, "Живая", "https://example.com/a", &f.ID, nil)
	deleted := mustBookmark(t, s, "Удалённая", "https://example.com/b", &f.ID, nil)
	if err := s.SoftDelete(testCtx, deleted.ID); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}

	without, err := s.ExportJSON(testCtx, false)
	if err != nil {
		t.Fatalf("ExportJSON(false): %v", err)
	}
	with, err := s.ExportJSON(testCtx, true)
	if err != nil {
		t.Fatalf("ExportJSON(true): %v", err)
	}
	if len(with) <= len(without) {
		t.Fatalf("экспорт с корзиной (%d) не больше обычного (%d)", len(with), len(without))
	}
	var doc ExportDoc
	if err := json.Unmarshal(with, &doc); err != nil {
		t.Fatalf("json: %v", err)
	}
	if len(doc.Nodes) != 1 || len(doc.Nodes[0].Children) != 2 {
		t.Fatalf("в экспорте с корзиной ожидалось 2 дочерних узла: %+v", doc.Nodes)
	}
}

func TestImportReplaceIsAtomic(t *testing.T) {
	s := newTestStore(t)
	f := mustFolder(t, s, "Существующая", nil)
	mustBookmark(t, s, "Существующая ссылка", "https://example.com", &f.ID, []string{"тег"})

	doc := []ImportNode{
		{Type: "folder", Title: "Новая", Children: []ImportNode{
			{Type: "note", Title: "сломанный тип"},
		}},
	}
	if err := s.ImportJSON(testCtx, doc, true); err == nil {
		t.Fatal("импорт с недопустимым типом должен падать")
	}

	tree, err := s.Tree(testCtx)
	if err != nil {
		t.Fatalf("Tree: %v", err)
	}
	if len(tree) != 1 || len(tree[0].Children) != 1 {
		t.Fatalf("после неудачного replace данные должны сохраниться: %+v", tree)
	}
	if tree[0].Title != "Существующая" {
		t.Fatalf("вернулось %q вместо исходной папки", tree[0].Title)
	}
	tags, err := s.Tags(testCtx)
	if err != nil {
		t.Fatalf("Tags: %v", err)
	}
	if len(tags) != 1 {
		t.Fatalf("теги потеряны после отката: %+v", tags)
	}
}

func TestImportReplaceSuccess(t *testing.T) {
	s := newTestStore(t)
	mustFolder(t, s, "Старое", nil)

	doc := []ImportNode{{Type: "folder", Title: "Импорт", Children: []ImportNode{
		{Type: "bookmark", Title: "Ссылка", URL: "https://example.com/new", Tags: TagNames{"новый"}},
	}}}
	if err := s.ImportJSON(testCtx, doc, true); err != nil {
		t.Fatalf("ImportJSON: %v", err)
	}
	tree, err := s.Tree(testCtx)
	if err != nil {
		t.Fatalf("Tree: %v", err)
	}
	if len(tree) != 1 || tree[0].Title != "Импорт" {
		t.Fatalf("старые данные не заменены: %+v", tree)
	}
	tags, err := s.Tags(testCtx)
	if err != nil {
		t.Fatalf("Tags: %v", err)
	}
	if len(tags) != 1 || tags[0].Name != "новый" {
		t.Fatalf("теги после замены: %+v", tags)
	}
}

func TestImportSkipsEmptyAndForbidden(t *testing.T) {
	s := newTestStore(t)
	doc := []ImportNode{
		{Title: "без типа и url"},
		{Type: "bookmark", Title: "", URL: "javascript:alert(1)"},
		{Type: "bookmark", Title: "", URL: "https://example.com/ok"},
	}
	if err := s.ImportJSON(testCtx, doc, false); err != nil {
		t.Fatalf("ImportJSON: %v", err)
	}
	list, err := s.LiveBookmarks(testCtx)
	if err != nil {
		t.Fatalf("LiveBookmarks: %v", err)
	}
	if len(list) != 1 || list[0].URL != "https://example.com/ok" || list[0].Title != "https://example.com/ok" {
		t.Fatalf("ожидалась одна закладка с заголовком-URL: %+v", list)
	}
}
