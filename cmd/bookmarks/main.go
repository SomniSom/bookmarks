package main

import (
	"log"
	"net/http"
	"os"

	"bookmarks/internal/backup"
	"bookmarks/internal/config"
	"bookmarks/internal/server"
	"bookmarks/internal/store"
)

func main() {
	cfg := config.FromEnv()
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("sqlite: %v", err)
	}
	defer st.Close()
	if err := st.EnsurePassword(cfg.BootstrapPassword); err != nil {
		log.Fatalf("%v", err)
	}
	if cfg.BackupDir != "" {
		_ = st.SetSetting("backup_dir", cfg.BackupDir)
	}
	srv, err := server.New(cfg, st)
	if err != nil {
		log.Fatalf("server: %v", err)
	}
	backup.StartTicker(st)
	log.Printf("слушаем http://%s", cfg.Listen)
	if err := http.ListenAndServe(cfg.Listen, srv.Handler()); err != nil {
		log.Println(err)
		os.Exit(1)
	}
}
