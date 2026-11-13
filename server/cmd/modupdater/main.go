package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"modupdater/internal/auth"
	"modupdater/internal/buildinfo"
	"modupdater/internal/config"
	"modupdater/internal/httpapi"
	"modupdater/internal/minecraft"
	"modupdater/internal/mods"
	"modupdater/internal/store"
)

func main() {
	os.Exit(run())
}

func run() int {
	fs := flag.NewFlagSet("modupdater", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	cfgFlag := fs.String("config", "", "config file")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return 2
	}
	path := *cfgFlag
	if path == "" {
		exe, err := os.Executable()
		if err != nil {
			fmt.Fprintln(os.Stderr, "invalid_config")
			return 1
		}
		path = filepath.Join(filepath.Dir(exe), "config.json")
	}
	cfg, err := config.Load(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid_config")
		return 1
	}
	if err := config.ApplyEnv(cfg, path); err != nil {
		fmt.Fprintln(os.Stderr, "invalid_config")
		return 1
	}
	for _, name := range []string{"archive", "client_mods", "client_configs", "releases", "revoked", "backups"} {
		dir := filepath.Join(cfg.DataDir, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fmt.Fprintln(os.Stderr, "invalid_config")
			return 1
		}
		if name == "backups" {
			if err := os.Chmod(dir, 0o755); err != nil {
				fmt.Fprintln(os.Stderr, "invalid_config")
				return 1
			}
		}
	}
	db, err := store.Open(filepath.Join(cfg.DataDir, "modupdater.db"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "internal")
		return 1
	}
	defer db.Close()
	au, err := auth.New(db)
	if err != nil {
		fmt.Fprintln(os.Stderr, "internal")
		return 1
	}
	generated, err := au.BootstrapRoot(cfg.RootAdmin.Username, cfg.RootAdmin.Password)
	if err != nil {
		fmt.Fprintln(os.Stderr, "internal")
		return 1
	}
	if generated != "" {
		fmt.Printf("root_password=%s\n", generated)
	}
	mc := minecraft.New(
		cfg.Minecraft.SystemdUnit,
		cfg.Minecraft.RestartWarning,
		cfg.Minecraft.RestartDelaySec,
		cfg.Minecraft.RCON.Host,
		cfg.Minecraft.RCON.Port,
		cfg.Minecraft.RCON.Password,
	)
	md := mods.New(db, cfg.DataDir)
	api := httpapi.New(cfg, au, md, mc)
	go md.RunDaily()
	go md.RunS3()
	fmt.Printf("version=%s\n", buildinfo.Version)
	if err := api.Listen(cfg.Listen); err != nil {
		fmt.Fprintln(os.Stderr, "internal")
		return 1
	}
	return 0
}
