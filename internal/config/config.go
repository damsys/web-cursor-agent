// Package config はサーバと管理コマンドが共有する設定ファイルを読む。
package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"

	"gopkg.in/yaml.v3"
)

var projectIDPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

// Project はブラウザから選べる作業ディレクトリである。
type Project struct {
	ID   string `yaml:"id"`
	Name string `yaml:"name"`
	Path string `yaml:"path"`
}

// Config は起動時に確定した設定である。相対パスは設定ファイルの場所で解決済みにする。
type Config struct {
	Listen       string    `yaml:"listen"`
	UsersFile    string    `yaml:"users_file"`
	StateDir     string    `yaml:"state_dir"`
	AgentCommand string    `yaml:"agent_command"`
	WebDir       string    `yaml:"web_dir"`
	AllowCIDRs   []string  `yaml:"allow_cidrs"`
	MacCheck     bool      `yaml:"-"`
	Projects     []Project `yaml:"projects"`
	Networks     []*net.IPNet
}

type fileConfig struct {
	Listen       string    `yaml:"listen"`
	UsersFile    string    `yaml:"users_file"`
	StateDir     string    `yaml:"state_dir"`
	AgentCommand string    `yaml:"agent_command"`
	WebDir       string    `yaml:"web_dir"`
	AllowCIDRs   []string  `yaml:"allow_cidrs"`
	MacCheck     *bool     `yaml:"mac_check"`
	Projects     []Project `yaml:"projects"`
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
		Projects:     file.Projects,
	}
	if file.MacCheck != nil {
		cfg.MacCheck = *file.MacCheck
	}
	if cfg.Listen == "" {
		cfg.Listen = "0.0.0.0:8787"
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
	cfg.UsersFile = resolve(dir, cfg.UsersFile)
	cfg.StateDir = resolve(dir, cfg.StateDir)
	cfg.WebDir = resolve(dir, cfg.WebDir)
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
	return nil
}

func resolve(base, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(base, path))
}
