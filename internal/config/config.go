// Package config はサーバと管理コマンドが共有する設定ファイルを読む。
package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	defaultDetachGrace = 30 * time.Minute
	defaultHTTPListen  = "0.0.0.0:8787"
	defaultTLSPort     = "8788"
)

var projectIDPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

// Project はブラウザから選べる作業ディレクトリである。
type Project struct {
	ID   string `yaml:"id"`
	Name string `yaml:"name"`
	Path string `yaml:"path"`
}

// AutoRename は対話ターン数に応じたセッション名の自動更新設定である。
type AutoRename struct {
	// Enabled が true のとき、閾値ターンで agent -p により命名し /rename する。
	Enabled bool
}

// TLS はアプリ内蔵 HTTPS とセットアップ用 CA 配布の設定である。
type TLS struct {
	// Listen は HTTPS の待受アドレス。未指定時は listen と同じホストでポート 8788。
	Listen     string
	CertFile   string
	KeyFile    string
	CAFile     string
	PublicHost string
}

// Enabled はサーバ証明書と秘密鍵が揃っているとき true である。
func (t TLS) Enabled() bool {
	return t.CertFile != "" && t.KeyFile != ""
}

// Config は起動時に確定した設定である。相対パスは設定ファイルの場所で解決済みにする。
type Config struct {
	Listen       string
	UsersFile    string
	StateDir     string
	AgentCommand string
	WebDir       string
	AllowCIDRs   []string
	MacCheck     bool
	// DetachGrace は WebSocket 切断後に agent を残す時間である。
	DetachGrace time.Duration
	AutoRename  AutoRename
	TLS         TLS
	Projects    []Project
	Networks    []*net.IPNet
}

type fileAutoRename struct {
	Enabled *bool `yaml:"enabled"`
}

type fileTLS struct {
	Listen     string `yaml:"listen"`
	CertFile   string `yaml:"cert_file"`
	KeyFile    string `yaml:"key_file"`
	CAFile     string `yaml:"ca_file"`
	PublicHost string `yaml:"public_host"`
}

type fileConfig struct {
	Listen       string          `yaml:"listen"`
	UsersFile    string          `yaml:"users_file"`
	StateDir     string          `yaml:"state_dir"`
	AgentCommand string          `yaml:"agent_command"`
	WebDir       string          `yaml:"web_dir"`
	AllowCIDRs   []string        `yaml:"allow_cidrs"`
	MacCheck     *bool           `yaml:"mac_check"`
	DetachGrace  string          `yaml:"detach_grace"`
	AutoRename   *fileAutoRename `yaml:"auto_rename"`
	TLS          *fileTLS        `yaml:"tls"`
	Projects     []Project       `yaml:"projects"`
}

// Load は設定ファイルを読み、相対パスを設定ファイルのディレクトリ基準で絶対パスにする。
func Load(path string) (Config, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return Config{}, fmt.Errorf("resolve config path: %w", err)
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	var file fileConfig
	if err := yaml.Unmarshal(raw, &file); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	dir := filepath.Dir(abs)
	cfg := Config{
		Listen:       file.Listen,
		UsersFile:    file.UsersFile,
		StateDir:     file.StateDir,
		AgentCommand: file.AgentCommand,
		WebDir:       file.WebDir,
		AllowCIDRs:   file.AllowCIDRs,
		MacCheck:     true,
		DetachGrace:  defaultDetachGrace,
		AutoRename:   AutoRename{Enabled: true},
		Projects:     file.Projects,
	}
	if file.MacCheck != nil {
		cfg.MacCheck = *file.MacCheck
	}
	if file.AutoRename != nil && file.AutoRename.Enabled != nil {
		cfg.AutoRename.Enabled = *file.AutoRename.Enabled
	}
	if file.DetachGrace != "" {
		grace, err := time.ParseDuration(file.DetachGrace)
		if err != nil {
			return Config{}, fmt.Errorf("invalid detach_grace %q: %w", file.DetachGrace, err)
		}
		if grace < 0 {
			return Config{}, fmt.Errorf("detach_grace must not be negative")
		}
		cfg.DetachGrace = grace
	}
	if cfg.Listen == "" {
		cfg.Listen = defaultHTTPListen
	}
	if _, _, err := net.SplitHostPort(cfg.Listen); err != nil {
		return Config{}, fmt.Errorf("invalid listen address %q: %w", cfg.Listen, err)
	}
	if cfg.UsersFile == "" {
		cfg.UsersFile = "users.yaml"
	}
	if cfg.StateDir == "" {
		cfg.StateDir = "var"
	}
	if cfg.AgentCommand == "" {
		cfg.AgentCommand = "agent"
	}
	if cfg.WebDir == "" {
		cfg.WebDir = "web"
	}
	if file.TLS != nil {
		cfg.TLS = TLS{
			Listen:     strings.TrimSpace(file.TLS.Listen),
			CertFile:   file.TLS.CertFile,
			KeyFile:    file.TLS.KeyFile,
			CAFile:     file.TLS.CAFile,
			PublicHost: strings.TrimSpace(file.TLS.PublicHost),
		}
	}
	if (cfg.TLS.CertFile == "") != (cfg.TLS.KeyFile == "") {
		return Config{}, fmt.Errorf("tls.cert_file and tls.key_file must be set together")
	}
	if cfg.TLS.Enabled() && cfg.TLS.PublicHost == "" {
		return Config{}, fmt.Errorf("tls.public_host is required when TLS is enabled")
	}
	if cfg.TLS.PublicHost != "" {
		if strings.Contains(cfg.TLS.PublicHost, "://") || strings.Contains(cfg.TLS.PublicHost, "/") {
			return Config{}, fmt.Errorf("tls.public_host must be a bare hostname")
		}
		if host, _, err := net.SplitHostPort(cfg.TLS.PublicHost); err == nil && host != "" {
			return Config{}, fmt.Errorf("tls.public_host must not include a port")
		}
	}
	if cfg.TLS.Enabled() {
		if cfg.TLS.Listen == "" {
			tlsListen, err := ReplaceListenPort(cfg.Listen, defaultTLSPort)
			if err != nil {
				return Config{}, err
			}
			cfg.TLS.Listen = tlsListen
		}
		if _, _, err := net.SplitHostPort(cfg.TLS.Listen); err != nil {
			return Config{}, fmt.Errorf("invalid tls.listen address %q: %w", cfg.TLS.Listen, err)
		}
		if cfg.TLS.Listen == cfg.Listen {
			return Config{}, fmt.Errorf("tls.listen must differ from listen (HTTP and HTTPS use separate ports)")
		}
	}
	cfg.UsersFile = resolve(dir, cfg.UsersFile)
	cfg.StateDir = resolve(dir, cfg.StateDir)
	cfg.WebDir = resolve(dir, cfg.WebDir)
	if cfg.TLS.CertFile != "" {
		cfg.TLS.CertFile = resolve(dir, cfg.TLS.CertFile)
	}
	if cfg.TLS.KeyFile != "" {
		cfg.TLS.KeyFile = resolve(dir, cfg.TLS.KeyFile)
	}
	if cfg.TLS.CAFile != "" {
		cfg.TLS.CAFile = resolve(dir, cfg.TLS.CAFile)
	}
	seen := map[string]struct{}{}
	for i := range cfg.Projects {
		project := &cfg.Projects[i]
		if !projectIDPattern.MatchString(project.ID) {
			return Config{}, fmt.Errorf("invalid project id %q", project.ID)
		}
		if _, ok := seen[project.ID]; ok {
			return Config{}, fmt.Errorf("duplicate project id %q", project.ID)
		}
		seen[project.ID] = struct{}{}
		if project.Name == "" {
			project.Name = project.ID
		}
		if project.Path == "" {
			return Config{}, fmt.Errorf("project %q has no path", project.ID)
		}
		project.Path = resolve(dir, project.Path)
	}
	for _, cidr := range cfg.AllowCIDRs {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			return Config{}, fmt.Errorf("invalid allow_cidrs entry %q", cidr)
		}
		cfg.Networks = append(cfg.Networks, network)
	}
	return cfg, nil
}

// Project は ID に一致するプロジェクトを返す。
func (c Config) Project(id string) (Project, bool) {
	for _, project := range c.Projects {
		if project.ID == id {
			return project, true
		}
	}
	return Project{}, false
}

// StaticDir は画面の HTML とスクリプトを置くディレクトリである。
func (c Config) StaticDir() string {
	return filepath.Join(c.WebDir, "static")
}

// VendorDir はビルド時にコピーした xterm の成果物を置くディレクトリである。
func (c Config) VendorDir() string {
	return filepath.Join(c.WebDir, "dist", "vendor")
}

// CheckRuntime は待受開始前に、プロジェクトと画面ファイルが存在することを確認する。
func (c Config) CheckRuntime() error {
	for _, project := range c.Projects {
		info, err := os.Stat(project.Path)
		if err != nil {
			return fmt.Errorf("project %q path: %w", project.ID, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("project %q path is not a directory", project.ID)
		}
	}
	index := filepath.Join(c.StaticDir(), "index.html")
	if _, err := os.Stat(index); err != nil {
		return fmt.Errorf("web static files: %w", err)
	}
	xterm := filepath.Join(c.VendorDir(), "@xterm", "xterm", "lib", "xterm.js")
	if _, err := os.Stat(xterm); err != nil {
		return fmt.Errorf("xterm assets are missing (run make -f Makefile.agent build): %w", err)
	}
	if c.TLS.Enabled() {
		if _, err := os.Stat(c.TLS.CertFile); err != nil {
			return fmt.Errorf("tls cert file: %w", err)
		}
		if _, err := os.Stat(c.TLS.KeyFile); err != nil {
			return fmt.Errorf("tls key file: %w", err)
		}
		if c.TLS.CAFile != "" {
			if _, err := os.Stat(c.TLS.CAFile); err != nil {
				return fmt.Errorf("tls CA file: %w", err)
			}
		}
	}
	return nil
}

// ListenPort は HTTP の listen 設定から待受ポートを返す。
func (c Config) ListenPort() (string, error) {
	_, port, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return "", fmt.Errorf("invalid listen address %q: %w", c.Listen, err)
	}
	return port, nil
}

// TLSListenPort は HTTPS の tls.listen から待受ポートを返す。
func (c Config) TLSListenPort() (string, error) {
	if !c.TLS.Enabled() {
		return "", fmt.Errorf("TLS is not enabled")
	}
	_, port, err := net.SplitHostPort(c.TLS.Listen)
	if err != nil {
		return "", fmt.Errorf("invalid tls.listen address %q: %w", c.TLS.Listen, err)
	}
	return port, nil
}

// ReplaceListenPort は host:port のポートだけを差し替える。
func ReplaceListenPort(addr, port string) (string, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("invalid listen address %q: %w", addr, err)
	}
	return net.JoinHostPort(host, port), nil
}

func resolve(base, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(base, path))
}
