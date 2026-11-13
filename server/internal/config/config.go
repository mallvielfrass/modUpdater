package config

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type Config struct {
	Listen    string
	DataDir   string
	PublicURL string
	RootAdmin RootAdmin
	Minecraft Minecraft
}

type RootAdmin struct {
	Username string
	Password string
}

type Minecraft struct {
	SystemdUnit     string
	RestartWarning  string
	RestartDelaySec int
	RCON            RCON
}

type RCON struct {
	Host     string
	Port     int
	Password string
}

type fileConfig struct {
	Listen    string `json:"listen"`
	DataDir   string `json:"data_dir"`
	PublicURL string `json:"public_url"`
	RootAdmin struct {
		Username string `json:"username"`
		Password string `json:"password"`
	} `json:"root_admin"`
	Minecraft struct {
		SystemdUnit     string `json:"systemd_unit"`
		RestartWarning  string `json:"restart_warning"`
		RestartDelaySec int    `json:"restart_delay_sec"`
		RCON            struct {
			Host     string `json:"host"`
			Port     int    `json:"port"`
			Password string `json:"password"`
		} `json:"rcon"`
	} `json:"minecraft"`
}

var (
	unitRe     = regexp.MustCompile(`^[A-Za-z0-9:._@-]+$`)
	usernameRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
)

func Load(path string) (*Config, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("invalid_config")
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return nil, fmt.Errorf("invalid_config")
	}
	var raw fileConfig
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("invalid_config")
	}
	if raw.Listen == "" || raw.DataDir == "" {
		return nil, fmt.Errorf("invalid_config")
	}
	public, err := normalizePublicURL(raw.PublicURL)
	if err != nil {
		return nil, err
	}
	if !unitRe.MatchString(raw.Minecraft.SystemdUnit) {
		return nil, fmt.Errorf("invalid_config")
	}
	if raw.Minecraft.RCON.Port < 1 || raw.Minecraft.RCON.Port > 65535 {
		return nil, fmt.Errorf("invalid_config")
	}
	if raw.Minecraft.RestartDelaySec < 0 || raw.Minecraft.RestartDelaySec > 120 {
		return nil, fmt.Errorf("invalid_config")
	}
	user := raw.RootAdmin.Username
	if user == "" {
		user = "admin"
	} else if len(user) < 3 || len(user) > 32 || !usernameRe.MatchString(user) {
		return nil, fmt.Errorf("invalid_config")
	}
	dataDir := raw.DataDir
	if !filepath.IsAbs(dataDir) {
		dataDir = filepath.Join(filepath.Dir(abs), dataDir)
	}
	dataDir, err = filepath.Abs(dataDir)
	if err != nil {
		return nil, fmt.Errorf("invalid_config")
	}
	return &Config{
		Listen:    raw.Listen,
		DataDir:   dataDir,
		PublicURL: public,
		RootAdmin: RootAdmin{Username: user, Password: raw.RootAdmin.Password},
		Minecraft: Minecraft{
			SystemdUnit:     raw.Minecraft.SystemdUnit,
			RestartWarning:  raw.Minecraft.RestartWarning,
			RestartDelaySec: raw.Minecraft.RestartDelaySec,
			RCON: RCON{
				Host:     raw.Minecraft.RCON.Host,
				Port:     raw.Minecraft.RCON.Port,
				Password: raw.Minecraft.RCON.Password,
			},
		},
	}, nil
}

func normalizePublicURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("invalid_config")
	}
	if u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("invalid_config")
	}
	if u.Path != "" && u.Path != "/" {
		return "", fmt.Errorf("invalid_config")
	}
	u.Path = ""
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	u.User = nil
	return u.String(), nil
}

// ApplyEnv replaces fields when the matching variable is non-empty.
// The server does not read a .env file itself.
func ApplyEnv(cfg *Config, configFile string) error {
	if v := strings.TrimSpace(os.Getenv("MODUPDATER_LISTEN")); v != "" {
		cfg.Listen = v
	}
	if v := strings.TrimSpace(os.Getenv("MODUPDATER_DATA_DIR")); v != "" {
		absCfg, err := filepath.Abs(configFile)
		if err != nil {
			return fmt.Errorf("invalid_config")
		}
		dataDir := v
		if !filepath.IsAbs(dataDir) {
			dataDir = filepath.Join(filepath.Dir(absCfg), dataDir)
		}
		dataDir, err = filepath.Abs(dataDir)
		if err != nil {
			return fmt.Errorf("invalid_config")
		}
		cfg.DataDir = dataDir
	}
	if v := strings.TrimSpace(os.Getenv("MODUPDATER_PUBLIC_URL")); v != "" {
		public, err := normalizePublicURL(v)
		if err != nil {
			return err
		}
		cfg.PublicURL = public
	}
	if v := strings.TrimSpace(os.Getenv("MODUPDATER_ROOT_USERNAME")); v != "" {
		if len(v) < 3 || len(v) > 32 || !usernameRe.MatchString(v) {
			return fmt.Errorf("invalid_config")
		}
		cfg.RootAdmin.Username = v
	}
	if v := os.Getenv("MODUPDATER_ROOT_PASSWORD"); v != "" {
		cfg.RootAdmin.Password = v
	}
	if v := strings.TrimSpace(os.Getenv("MODUPDATER_SYSTEMD_UNIT")); v != "" {
		if !unitRe.MatchString(v) {
			return fmt.Errorf("invalid_config")
		}
		cfg.Minecraft.SystemdUnit = v
	}
	if v := os.Getenv("MODUPDATER_RESTART_WARNING"); strings.TrimSpace(v) != "" {
		cfg.Minecraft.RestartWarning = v
	}
	if v := strings.TrimSpace(os.Getenv("MODUPDATER_RESTART_DELAY_SEC")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > 120 {
			return fmt.Errorf("invalid_config")
		}
		cfg.Minecraft.RestartDelaySec = n
	}
	if v := strings.TrimSpace(os.Getenv("MODUPDATER_RCON_HOST")); v != "" {
		cfg.Minecraft.RCON.Host = v
	}
	if v := strings.TrimSpace(os.Getenv("MODUPDATER_RCON_PORT")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("invalid_config")
		}
		cfg.Minecraft.RCON.Port = n
	}
	if v := os.Getenv("MODUPDATER_RCON_PASSWORD"); v != "" {
		cfg.Minecraft.RCON.Password = v
	}
	if cfg.Listen == "" || cfg.DataDir == "" {
		return fmt.Errorf("invalid_config")
	}
	return nil
}
