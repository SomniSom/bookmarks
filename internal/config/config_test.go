package config

import (
	"testing"
)

// envKeys перечисляет все переменные окружения, которые читает пакет.
// Список используется тестами для гарантированной изоляции от окружения машины.
var envKeys = []string{
	"LISTEN",
	"DB_PATH",
	"BOOKMARKS_PASSWORD",
	"COOKIE_SECRET",
	"TRUSTED_PROXY",
	"BACKUP_DIR",
}

// cleanEnv очищает все переменные, читаемые пакетом, от внешнего окружения.
func cleanEnv(t *testing.T) {
	t.Helper()
	for _, key := range envKeys {
		t.Setenv(key, "")
	}
}

func TestFromEnvDefaults(t *testing.T) {
	cleanEnv(t)

	c := FromEnv()

	if c.Listen != "127.0.0.1:8080" {
		t.Errorf("LISTEN: ожидали дефолт %q, получили %q", "127.0.0.1:8080", c.Listen)
	}
	if c.DBPath != "./data/bookmarks.sqlite" {
		t.Errorf("DB_PATH: ожидали дефолт %q, получили %q", "./data/bookmarks.sqlite", c.DBPath)
	}
	if c.BootstrapPassword != "" {
		t.Errorf("BOOKMARKS_PASSWORD: ожидали пустую строку, получили %q", c.BootstrapPassword)
	}
	if c.CookieSecret != "" {
		t.Errorf("COOKIE_SECRET: ожидали пустую строку, получили %q", c.CookieSecret)
	}
	if c.TrustedProxy {
		t.Error("TRUSTED_PROXY: ожидали false по умолчанию, получили true")
	}
	if c.BackupDir != "./backups" {
		t.Errorf("BACKUP_DIR: ожидали дефолт %q, получили %q", "./backups", c.BackupDir)
	}
}

func TestFromEnvAllSet(t *testing.T) {
	t.Run("все переменные заданы и проброшены", func(t *testing.T) {
		cleanEnv(t)
		t.Setenv("LISTEN", ":9090")
		t.Setenv("DB_PATH", "/tmp/custom.sqlite")
		t.Setenv("BOOKMARKS_PASSWORD", "p@ss")
		t.Setenv("COOKIE_SECRET", "s3cret")
		t.Setenv("TRUSTED_PROXY", "true")
		t.Setenv("BACKUP_DIR", "/var/backups")

		c := FromEnv()

		if c.Listen != ":9090" {
			t.Errorf("LISTEN: ожидали %q, получили %q", ":9090", c.Listen)
		}
		if c.DBPath != "/tmp/custom.sqlite" {
			t.Errorf("DB_PATH: ожидали %q, получили %q", "/tmp/custom.sqlite", c.DBPath)
		}
		if c.BootstrapPassword != "p@ss" {
			t.Errorf("BOOKMARKS_PASSWORD: ожидали %q, получили %q", "p@ss", c.BootstrapPassword)
		}
		if c.CookieSecret != "s3cret" {
			t.Errorf("COOKIE_SECRET: ожидали %q, получили %q", "s3cret", c.CookieSecret)
		}
		if !c.TrustedProxy {
			t.Error("TRUSTED_PROXY: ожидали true, получили false")
		}
		if c.BackupDir != "/var/backups" {
			t.Errorf("BACKUP_DIR: ожидали %q, получили %q", "/var/backups", c.BackupDir)
		}
	})

	t.Run("пробелы по краям обрезаются", func(t *testing.T) {
		cleanEnv(t)
		t.Setenv("LISTEN", "  :8080  ")
		t.Setenv("DB_PATH", " \t/data/x.sqlite \n")
		t.Setenv("BACKUP_DIR", "  ./bk  ")
		t.Setenv("TRUSTED_PROXY", " 1 ")

		c := FromEnv()

		if c.Listen != ":8080" {
			t.Errorf("LISTEN: ожидали %q без пробелов, получили %q", ":8080", c.Listen)
		}
		if c.DBPath != "/data/x.sqlite" {
			t.Errorf("DB_PATH: ожидали %q без пробелов, получили %q", "/data/x.sqlite", c.DBPath)
		}
		if c.BackupDir != "./bk" {
			t.Errorf("BACKUP_DIR: ожидали %q без пробелов, получили %q", "./bk", c.BackupDir)
		}
		if !c.TrustedProxy {
			t.Error("TRUSTED_PROXY: ожидали true после обрезки пробелов")
		}
	})

	t.Run("пароль и секрет не обрезаются — берутся как есть", func(t *testing.T) {
		cleanEnv(t)
		t.Setenv("BOOKMARKS_PASSWORD", " pa ss ")
		t.Setenv("COOKIE_SECRET", "  secret  ")

		c := FromEnv()

		if c.BootstrapPassword != " pa ss " {
			t.Errorf("BOOKMARKS_PASSWORD: ожидали как есть %q, получили %q", " pa ss ", c.BootstrapPassword)
		}
		if c.CookieSecret != "  secret  " {
			t.Errorf("COOKIE_SECRET: ожидали как есть %q, получили %q", "  secret  ", c.CookieSecret)
		}
	})
}

func TestFromEnvTrustedProxy(t *testing.T) {
	tests := map[string]struct {
		value    string
		expected bool
	}{
		"единица": {"1", true},
		"true":    {"true", true},
		"TRUE в верхнем регистре": {"TRUE", true},
		"yes":              {"yes", true},
		"ноль":             {"0", false},
		"false":            {"false", false},
		"мусорные данные":  {"garbage", false},
		"пусто":            {"", false},
		"пробелы вокруг 1": {"  1  ", true},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cleanEnv(t)
			t.Setenv("TRUSTED_PROXY", tt.value)

			c := FromEnv()

			if c.TrustedProxy != tt.expected {
				t.Errorf("TRUSTED_PROXY=%q: ожидали %v, получили %v", tt.value, tt.expected, c.TrustedProxy)
			}
		})
	}
}

func TestEnv(t *testing.T) {
	tests := map[string]struct {
		value    string
		fallback string
		expected string
	}{
		"пустое значение":  {"", "fb", "fb"},
		"только пробелы":   {"   ", "fb", "fb"},
		"пробелы и табы":   {" \t\n ", "fb", "fb"},
		"значение есть":    {"val", "fb", "val"},
		"обрезает пробелы": {"  val  ", "fb", "val"},
		"пустой фолбэк":    {"", "", ""},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Setenv("TEST_ENV_KEY", tt.value)

			got := env("TEST_ENV_KEY", tt.fallback)

			if got != tt.expected {
				t.Errorf("env(value=%q, fallback=%q): ожидали %q, получили %q", tt.value, tt.fallback, tt.expected, got)
			}
		})
	}
}

func TestParseBool(t *testing.T) {
	tests := map[string]struct {
		value    string
		expected bool
	}{
		"пусто":              {"", false},
		"только пробелы":     {"   ", false},
		"единица":            {"1", true},
		"true":               {"true", true},
		"TRUE":               {"TRUE", true},
		"yes":                {"yes", true},
		"пробелы вокруг yes": {"  yes  ", true},
		"прописное No":       {"No", false},
		"on":                 {"on", false},
		"off":                {"off", false},
		"ноль":               {"0", false},
		"false":              {"false", false},
		"мусор":              {"garbage", false},
		"T (регистр игнорируется)": {"T", true},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got := parseBool(tt.value)

			if got != tt.expected {
				t.Errorf("parseBool(%q): ожидали %v, получили %v", tt.value, tt.expected, got)
			}
		})
	}
}
