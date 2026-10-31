package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"bookmarks/internal/backup"
	"bookmarks/internal/config"
	"bookmarks/internal/netguard"
	"bookmarks/internal/server"
	"bookmarks/internal/store"
)

func main() {
	cfg := config.FromEnv()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	netguard.SetBlockPrivate(cfg.BlockPrivateURLs)

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("sqlite: %v", err)
	}
	defer func() { _ = st.Close() }()
	if err := st.EnsurePassword(ctx, cfg.BootstrapPassword); err != nil {
		log.Fatalf("%v", err)
	}
	if cfg.BackupDir != "" {
		_ = st.SetSetting(ctx, "backup_dir", cfg.BackupDir)
	}
	srv, err := server.New(cfg, st)
	if err != nil {
		log.Fatalf("server: %v", err)
	}
	backup.StartTicker(ctx, st)

	httpSrv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      90 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    1 << 20,
	}
	go func() {
		log.Printf("слушаем http://%s", cfg.Listen)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("http: %v", err)
			stop()
		}
	}()

	<-ctx.Done()
	log.Println("останавливаемся")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}
