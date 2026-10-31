package backup

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"bookmarks/internal/store"
)

func Run(ctx context.Context, st *store.Store) (string, string, error) {
	dir, _ := st.Setting(ctx, "backup_dir")
	if strings.TrimSpace(dir) == "" {
		dir = "./backups"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", err
	}
	ts := time.Now().UTC().Format("20060102T150405")
	jsonPath := filepath.Join(dir, "bookmarks-"+ts+".json")
	sqlitePath := filepath.Join(dir, "bookmarks-"+ts+".sqlite")
	data, err := st.ExportJSON(ctx, false)
	if err != nil {
		return "", "", err
	}
	if err := os.WriteFile(jsonPath, data, 0o600); err != nil {
		return "", "", err
	}
	if err := st.VacuumInto(ctx, sqlitePath); err != nil {
		return jsonPath, "", err
	}
	_ = st.SetSetting(ctx, "last_backup_at", time.Now().UTC().Format(time.RFC3339Nano))
	prune(dir, retention(ctx, st))
	return jsonPath, sqlitePath, nil
}

func StartTicker(ctx context.Context, st *store.Store) {
	t := time.NewTicker(time.Minute)
	go func() {
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			hours := intervalHours(ctx, st)
			if hours <= 0 {
				continue
			}
			last, _ := st.Setting(ctx, "last_backup_at")
			if last != "" {
				when, err := time.Parse(time.RFC3339Nano, last)
				if err == nil && time.Since(when) < time.Duration(hours)*time.Hour {
					continue
				}
			}
			if _, _, err := Run(ctx, st); err != nil {
				log.Printf("backup: %v", err)
			}
		}
	}()
}

func intervalHours(ctx context.Context, st *store.Store) int {
	v, _ := st.Setting(ctx, "backup_interval_hours")
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return 24
	}
	return n
}

func retention(ctx context.Context, st *store.Store) int {
	v, _ := st.Setting(ctx, "backup_retention")
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 1 {
		return 14
	}
	return n
}

func prune(dir string, keep int) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var jsons, sqlites []string
	for _, e := range ents {
		name := e.Name()
		p := filepath.Join(dir, name)
		switch {
		case strings.HasPrefix(name, "bookmarks-") && strings.HasSuffix(name, ".json"):
			jsons = append(jsons, p)
		case strings.HasPrefix(name, "bookmarks-") && strings.HasSuffix(name, ".sqlite"):
			sqlites = append(sqlites, p)
		}
	}
	sort.Strings(jsons)
	sort.Strings(sqlites)
	dropOldest(jsons, keep)
	dropOldest(sqlites, keep)
}

func dropOldest(files []string, keep int) {
	if len(files) <= keep {
		return
	}
	for _, f := range files[:len(files)-keep] {
		_ = os.Remove(f)
	}
}
