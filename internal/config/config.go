package config

import (
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Listen            string
	DBPath            string
	BootstrapPassword string
	CookieSecret      string
	TrustedProxy      bool
	BackupDir         string
}

func FromEnv() Config {
	c := Config{
		Listen:            env("LISTEN", "127.0.0.1:8080"),
		DBPath:            env("DB_PATH", "./data/bookmarks.sqlite"),
		BootstrapPassword: os.Getenv("BOOKMARKS_PASSWORD"),
		CookieSecret:      os.Getenv("COOKIE_SECRET"),
		TrustedProxy:      parseBool(os.Getenv("TRUSTED_PROXY")),
		BackupDir:         env("BACKUP_DIR", "./backups"),
	}
	return c
}

func env(key, fallback string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	return v
}

func parseBool(v string) bool {
	v = strings.TrimSpace(strings.ToLower(v))
	if v == "" {
		return false
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return v == "1" || v == "yes"
	}
	return b
}
