package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func baseJSON() string {
	return `{
  "listen": ":8080",
  "data_dir": "./data",
  "public_url": "http://127.0.0.1:8080",
  "root_admin": { "username": "admin", "password": "" },
  "minecraft": {
    "systemd_unit": "minecraft.service",
    "restart_warning": "Перезапуск сервера через 15 секунд: обновление модов",
    "restart_delay_sec": 15,
    "rcon": { "host": "127.0.0.1", "port": 25575, "password": "" }
  }
}`
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRelativeDataDirFromConfigFile(t *testing.T) {
	path := writeConfig(t, baseJSON())
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(filepath.Dir(path), "data")
	want, err = filepath.Abs(want)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DataDir != want {
		t.Fatalf("data_dir %s, want %s", cfg.DataDir, want)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DataDir == filepath.Join(cwd, "data") && filepath.Clean(cwd) != filepath.Clean(filepath.Dir(path)) {
		t.Fatal("resolved from process cwd")
	}
	if cfg.PublicURL != "http://127.0.0.1:8080" {
		t.Fatal(cfg.PublicURL)
	}
}

func TestPublicURLTrailingSlashStripped(t *testing.T) {
	body := strings.Replace(baseJSON(), "http://127.0.0.1:8080", "http://127.0.0.1:8080/", 1)
	cfg, err := Load(writeConfig(t, body))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PublicURL != "http://127.0.0.1:8080" {
		t.Fatal(cfg.PublicURL)
	}
}

func TestRejectsBadConfig(t *testing.T) {
	cases := []string{
		strings.Replace(baseJSON(), "http://127.0.0.1:8080", "http://127.0.0.1:8080/mods", 1),
		strings.Replace(baseJSON(), "http://127.0.0.1:8080", "http://user:pass@127.0.0.1:8080", 1),
		strings.Replace(baseJSON(), "minecraft.service", "bad unit", 1),
		strings.Replace(baseJSON(), `"restart_delay_sec": 15`, `"restart_delay_sec": 121`, 1),
		strings.Replace(baseJSON(), `"restart_delay_sec": 15`, `"restart_delay_sec": -1`, 1),
	}
	for _, body := range cases {
		if _, err := Load(writeConfig(t, body)); err == nil {
			t.Fatalf("accepted %s", body)
		}
	}
}

func TestApplyEnvOverridesSecrets(t *testing.T) {
	t.Setenv("MODUPDATER_ROOT_PASSWORD", "from-env-secret")
	t.Setenv("MODUPDATER_RCON_PASSWORD", "rcon-env-secret")
	t.Setenv("MODUPDATER_PUBLIC_URL", "http://127.0.0.1:9090/")
	t.Setenv("MODUPDATER_RCON_PORT", "25576")
	path := writeConfig(t, baseJSON())
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RootAdmin.Password != "" || cfg.Minecraft.RCON.Password != "" {
		t.Fatal("json passwords leaked before env apply")
	}
	if err := ApplyEnv(cfg, path); err != nil {
		t.Fatal(err)
	}
	if cfg.RootAdmin.Password != "from-env-secret" || cfg.Minecraft.RCON.Password != "rcon-env-secret" {
		t.Fatal(cfg.RootAdmin.Password, cfg.Minecraft.RCON.Password)
	}
	if cfg.PublicURL != "http://127.0.0.1:9090" || cfg.Minecraft.RCON.Port != 25576 {
		t.Fatal(cfg.PublicURL, cfg.Minecraft.RCON.Port)
	}
}

func TestDelayBounds(t *testing.T) {
	body := strings.Replace(baseJSON(), `"restart_delay_sec": 15`, `"restart_delay_sec": 0`, 1)
	if _, err := Load(writeConfig(t, body)); err != nil {
		t.Fatal(err)
	}
	body = strings.Replace(baseJSON(), `"restart_delay_sec": 15`, `"restart_delay_sec": 120`, 1)
	if _, err := Load(writeConfig(t, body)); err != nil {
		t.Fatal(err)
	}
}
