package backup

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"bookmarks/internal/store"
)

var testCtx = context.Background()

type exportDoc struct {
	Nodes []*store.Node `json:"nodes"`
}

func newStore(t *testing.T, extraNodes int) *store.Store {
	t.Helper()
	s, err := store.Open(t.TempDir() + "/t.sqlite")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	for i := range extraNodes {
		_, err := s.CreateNode(testCtx, &store.Node{
			Type:  "bookmark",
			Title: "Заголовок " + string(rune('A'+i)),
			URL:   "https://example.com/" + string(rune('a'+i)),
		}, nil)
		if err != nil {
			t.Fatalf("CreateNode: %v", err)
		}
	}
	return s
}

func countBackups(t *testing.T, dir string) (jsonCnt, sqliteCnt int) {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range ents {
		n := e.Name()
		switch {
		case strings.HasPrefix(n, "bookmarks-") && strings.HasSuffix(n, ".json"):
			jsonCnt++
		case strings.HasPrefix(n, "bookmarks-") && strings.HasSuffix(n, ".sqlite"):
			sqliteCnt++
		}
	}
	return
}

func TestRun_SozdaetObaFayla(t *testing.T) {
	s := newStore(t, 3)
	dir := t.TempDir()
	if err := s.SetSetting(testCtx, "backup_dir", dir); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	jsonPath, sqlitePath, err := Run(testCtx, s)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if jsonPath == "" || sqlitePath == "" {
		t.Fatal("Run вернул пустые пути")
	}
	if filepath.Dir(jsonPath) != dir || filepath.Dir(sqlitePath) != dir {
		t.Fatalf("файлы не в заданном каталоге: %s, %s", jsonPath, sqlitePath)
	}

	if fi, err := os.Stat(jsonPath); err != nil || fi.Size() == 0 {
		t.Fatalf("json-файл не создан: %v", err)
	}
	if fi, err := os.Stat(sqlitePath); err != nil || fi.Size() == 0 {
		t.Fatalf("sqlite-файл не создан: %v", err)
	}

	data, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("ReadFile json: %v", err)
	}
	var doc exportDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("JSON невалиден: %v", err)
	}
	if len(doc.Nodes) != 3 {
		t.Fatalf("JSON содержит %d узлов, ожидалось 3", len(doc.Nodes))
	}

	s2, err := store.Open(sqlitePath)
	if err != nil {
		t.Fatalf("Open sqlite-копии: %v", err)
	}
	defer func() { _ = s2.Close() }()
	tree, err := s2.Tree(testCtx)
	if err != nil {
		t.Fatalf("Tree копии: %v", err)
	}
	if len(tree) != 3 {
		t.Fatalf("копия содержит %d узлов, ожидалось 3", len(tree))
	}

	last, _ := s.Setting(testCtx, "last_backup_at")
	if last == "" {
		t.Fatal("last_backup_at не заполнен")
	}
	if _, err := time.Parse(time.RFC3339Nano, last); err != nil {
		t.Fatalf("last_backup_at не парсится как RFC3339Nano: %q", last)
	}
}

func TestRun_SozdaetVlozhennyyKatalog(t *testing.T) {
	s := newStore(t, 1)
	dir := t.TempDir() + "/a/b/c"
	if err := s.SetSetting(testCtx, "backup_dir", dir); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	if _, _, err := Run(testCtx, s); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		t.Fatalf("вложенный каталог не создан: %v", err)
	}
	if j, sq := countBackups(t, dir); j != 1 || sq != 1 {
		t.Fatalf("ожидалось 1 json и 1 sqlite, получил %d и %d", j, sq)
	}
}

func TestRun_RetenciaJSON(t *testing.T) {
	s := newStore(t, 1)
	dir := t.TempDir()
	if err := s.SetSetting(testCtx, "backup_dir", dir); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	// retention=2: после трёх прогонов должно остаться 2 json и 2 sqlite.
	if err := s.SetSetting(testCtx, "backup_retention", "2"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	for i := range 3 {
		if _, _, err := Run(testCtx, s); err != nil {
			t.Fatalf("Run #%d: %v", i, err)
		}
		// Гарантируем разные метки времени (формат до секунды).
		time.Sleep(1100 * time.Millisecond)
	}
	j, sq := countBackups(t, dir)
	if j > 2 || sq > 2 {
		t.Fatalf("после prune осталось %d json и %d sqlite, ожидалось не более 2", j, sq)
	}
}

func TestIntervalHours(t *testing.T) {
	s := newStore(t, 0)
	cases := []struct {
		name string
		val  string
		want int
	}{
		{"IzNastroek", "3", 3},
		{"MusorHead", "abc", 24},
		{"Pustaya", "", 24},
		{"Nol", "0", 0},
		{"Otritsatelnoe", "-5", -5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := s.SetSetting(testCtx, "backup_interval_hours", tc.val); err != nil {
				t.Fatalf("SetSetting: %v", err)
			}
			if got := intervalHours(testCtx, s); got != tc.want {
				t.Fatalf("intervalHours(%q)=%d, ожидалось %d", tc.val, got, tc.want)
			}
		})
	}
}

func TestRetention(t *testing.T) {
	s := newStore(t, 0)
	cases := []struct {
		name string
		val  string
		want int
	}{
		{"IzNastroek", "5", 5},
		{"MusorHead", "abc", 14},
		{"Pustaya", "", 14},
		{"Nol", "0", 14},
		{"Otritsatelnoe", "-5", 14},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := s.SetSetting(testCtx, "backup_retention", tc.val); err != nil {
				t.Fatalf("SetSetting: %v", err)
			}
			if got := retention(testCtx, s); got != tc.want {
				t.Fatalf("retention(%q)=%d, ожидалось %d", tc.val, got, tc.want)
			}
		})
	}
}

func TestPrune_NeTrogaetPostoronnie(t *testing.T) {
	dir := t.TempDir()
	write := func(name string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("bookmarks-20240101T000000.json")
	write("bookmarks-20240101T000000.sqlite")
	write("bookmarks-20240102T000000.json")
	write("bookmarks-20240102T000000.sqlite")
	write("notes.txt")
	write("bookmarks-noext")

	prune(dir, 1)

	if _, err := os.Stat(filepath.Join(dir, "notes.txt")); err != nil {
		t.Fatal("prune удалил посторонний файл notes.txt")
	}
	if _, err := os.Stat(filepath.Join(dir, "bookmarks-noext")); err != nil {
		t.Fatal("prune удалил файл без расширения")
	}
	if j, sq := countBackups(t, dir); j != 1 || sq != 1 {
		t.Fatalf("после prune осталось %d json и %d sqlite, ожидалось по 1", j, sq)
	}
}

func TestPrune_KeepBolsheKolichestva(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{
		"bookmarks-20240101T000000.json",
		"bookmarks-20240101T000000.sqlite",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	prune(dir, 10)
	if j, sq := countBackups(t, dir); j != 1 || sq != 1 {
		t.Fatalf("keep>кол-во удалил файлы: %d json, %d sqlite", j, sq)
	}
}

func TestPrune_KeepNol(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{
		"bookmarks-20240101T000000.json",
		"bookmarks-20240101T000000.sqlite",
		"bookmarks-20240102T000000.json",
		"notes.txt",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	prune(dir, 0)
	if j, sq := countBackups(t, dir); j != 0 || sq != 0 {
		t.Fatalf("keep=0 не удалил все: %d json, %d sqlite", j, sq)
	}
	if _, err := os.Stat(filepath.Join(dir, "notes.txt")); err != nil {
		t.Fatal("keep=0 удалил посторонний файл")
	}
}

func TestDropOldest(t *testing.T) {
	dir := t.TempDir()
	files := []string{
		filepath.Join(dir, "b.json"),
		filepath.Join(dir, "a.json"),
		filepath.Join(dir, "c.json"),
	}
	for _, f := range files {
		if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	// Лексическая сортировка a < b < c, всё отсортировано заранее.
	dropOldest([]string{files[1], files[0], files[2]}, 1)
	var remain []string
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		remain = append(remain, e.Name())
	}
	if len(remain) != 1 || remain[0] != "c.json" {
		t.Fatalf("dropOldest оставил %v, ожидался [c.json]", remain)
	}
}

func TestStartTicker_OtmenennyyKonteka(t *testing.T) {
	s := newStore(t, 1)
	dir := t.TempDir()
	if err := s.SetSetting(testCtx, "backup_dir", dir); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		StartTicker(ctx, s)
	}()
	select {
	case <-done:
		// вернулся сразу — ок
	case <-time.After(2 * time.Second):
		t.Fatal("StartTicker с отменённым ctx не вернулся")
	}
	// Дать горутине выйти до закрытия store в cleanup.
	time.Sleep(50 * time.Millisecond)
	if j, sq := countBackups(t, dir); j != 0 || sq != 0 {
		t.Fatalf("бэкапы созданы при отменённом ctx: %d json, %d sqlite", j, sq)
	}
}

func TestStartTicker_NolIntervalsNeSozdaetBackapov(t *testing.T) {
	s := newStore(t, 1)
	dir := t.TempDir()
	if err := s.SetSetting(testCtx, "backup_dir", dir); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	if err := s.SetSetting(testCtx, "backup_interval_hours", "0"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	StartTicker(ctx, s)
	time.Sleep(150 * time.Millisecond)
	cancel()
	// Дать горутине выйти до закрытия store в cleanup.
	time.Sleep(50 * time.Millisecond)
	if j, sq := countBackups(t, dir); j != 0 || sq != 0 {
		t.Fatalf("при interval=0 созданы бэкапы: %d json, %d sqlite", j, sq)
	}
}
