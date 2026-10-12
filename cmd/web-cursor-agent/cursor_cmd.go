package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"

	"web-cursor-agent/internal/config"
	"web-cursor-agent/internal/cursor"
	"web-cursor-agent/internal/users"
)

func cursorCommand(args []string, action string) int {
	return runUserEnvCommand(args, "cursor-"+action, func(cfg config.Config, layout cursor.Layout) (*exec.Cmd, error) {
		command := cfg.AgentCommand
		if _, err := exec.LookPath(command); err != nil && !strings.Contains(command, "/") {
			return nil, fmt.Errorf("agent command: %w", err)
		}
		cmd := exec.Command(command, action)
		cmd.Env = layout.Environ(os.Environ())
		return cmd, nil
	})
}

// cursorAutoWebSearch はユーザーの CURSOR_CONFIG_DIR にある cli-config.json で
// WebSearch の自動承認 (autoAcceptWebSearch) を有効にする。
func cursorAutoWebSearch(args []string) int {
	fs := flag.NewFlagSet("cursor-auto-websearch", flag.ContinueOnError)
	configPath := fs.String("config", "config.yaml", "path to config.yaml")
	username := fs.String("username", "", "user name")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *username == "" {
		log.Printf("username is required")
		return 2
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Printf("load config: %v", err)
		return 1
	}
	file, err := users.Load(cfg.UsersFile)
	if err != nil {
		log.Printf("load users: %v", err)
		return 1
	}
	if _, ok := file.Find(*username); !ok {
		log.Printf("unknown user %q", *username)
		return 1
	}
	layout, err := cursor.LayoutFor(cfg.StateDir, *username)
	if err != nil {
		log.Printf("cursor home: %v", err)
		return 1
	}
	if err := layout.Ensure(); err != nil {
		log.Printf("cursor home: %v", err)
		return 1
	}
	if err := cursor.EnsureAutoAcceptWebSearch(layout.ConfigDir); err != nil {
		log.Printf("enable autoAcceptWebSearch: %v", err)
		return 1
	}
	fmt.Printf("WebSearch の自動承認を有効にしました: %s\n", *username)
	return 0
}

// ghCommand はユーザーごとの XDG_CONFIG_HOME で GitHub CLI を動かす。
func ghCommand(args []string, action string) int {
	return runUserEnvCommand(args, "gh-"+action, func(_ config.Config, layout cursor.Layout) (*exec.Cmd, error) {
		if _, err := exec.LookPath("gh"); err != nil {
			return nil, fmt.Errorf("gh command: %w", err)
		}
		cmd := exec.Command("gh", "auth", action)
		cmd.Env = layout.Environ(os.Environ())
		return cmd, nil
	})
}

// runUserEnvCommand は指定ユーザーの Cursor 状態ディレクトリを用意し、その環境で外部コマンドを実行する。
func runUserEnvCommand(args []string, name string, build func(config.Config, cursor.Layout) (*exec.Cmd, error)) int {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	configPath := fs.String("config", "config.yaml", "path to config.yaml")
	username := fs.String("username", "", "user name")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *username == "" {
		log.Printf("username is required")
		return 2
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Printf("load config: %v", err)
		return 1
	}
	file, err := users.Load(cfg.UsersFile)
	if err != nil {
		log.Printf("load users: %v", err)
		return 1
	}
	if _, ok := file.Find(*username); !ok {
		log.Printf("unknown user %q", *username)
		return 1
	}
	layout, err := cursor.LayoutFor(cfg.StateDir, *username)
	if err != nil {
		log.Printf("cursor home: %v", err)
		return 1
	}
	if err := layout.Ensure(); err != nil {
		log.Printf("cursor home: %v", err)
		return 1
	}
	cmd, err := build(cfg, layout)
	if err != nil {
		log.Printf("%v", err)
		return 1
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Printf("%s: %v", name, err)
		return 1
	}
	return 0
}
