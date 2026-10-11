package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/crypto/bcrypt"

	"web-cursor-agent/internal/config"
	"web-cursor-agent/internal/cursor"
	"web-cursor-agent/internal/localtls"
	"web-cursor-agent/internal/password"
	"web-cursor-agent/internal/server"
	"web-cursor-agent/internal/users"
)

func main() {
	log.SetFlags(log.LstdFlags)
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var code int
	switch os.Args[1] {
	case "serve":
		code = serve(os.Args[2:])
	case "user":
		code = userCommand(os.Args[2:])
	case "tls":
		code = tlsCommand(os.Args[2:])
	case "cursor-login":
		code = cursorCommand(os.Args[2:], "login")
	case "cursor-status":
		code = cursorCommand(os.Args[2:], "status")
	case "cursor-auto-websearch":
		code = cursorAutoWebSearch(os.Args[2:])
	case "gh-login":
		code = ghCommand(os.Args[2:], "login")
	case "gh-status":
		code = ghCommand(os.Args[2:], "status")
	case "help", "-h", "--help":
		usage()
		code = 0
	default:
		usage()
		code = 2
	}
	os.Exit(code)
}

func usage() {
	fmt.Fprintf(os.Stderr, `使い方:
  web-cursor-agent serve --config config.yaml
  web-cursor-agent user upsert --config config.yaml --username NAME [--mac MAC]... [--clear-macs]
  web-cursor-agent user list --config config.yaml
  web-cursor-agent tls init --config config.yaml [--hosts NAME]... [--force]
  web-cursor-agent tls reissue --config config.yaml --hosts NAME [--hosts NAME]...
  web-cursor-agent cursor-login --config config.yaml --username NAME
  web-cursor-agent cursor-status --config config.yaml --username NAME
  web-cursor-agent cursor-auto-websearch --config config.yaml --username NAME
  web-cursor-agent gh-login --config config.yaml --username NAME
  web-cursor-agent gh-status --config config.yaml --username NAME
`)
}

func serve(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	configPath := fs.String("config", "config.yaml", "path to config.yaml")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Printf("load config: %v", err)
		return 1
	}
	if err := cfg.CheckRuntime(); err != nil {
		log.Printf("config: %v", err)
		return 1
	}
	if err := os.MkdirAll(cfg.StateDir, 0o700); err != nil {
		log.Printf("create state dir: %v", err)
		return 1
	}
	app := server.New(cfg)
	app.BindLaunch()
	httpEndpoints, err := listenEndpoints(cfg.Listen)
	if err != nil {
		log.Printf("listen address: %v", err)
		return 1
	}
	var tlsConfig *tls.Config
	var tlsEndpoints []listenEndpoint
	if cfg.TLS.Enabled() {
		cert, err := tls.LoadX509KeyPair(cfg.TLS.CertFile, cfg.TLS.KeyFile)
		if err != nil {
			log.Printf("load TLS certificate: %v", err)
			return 1
		}
		tlsConfig = &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		}
		tlsEndpoints, err = listenEndpoints(cfg.TLS.Listen)
		if err != nil {
			log.Printf("tls.listen address: %v", err)
			return 1
		}
	}
	handler := app.Handler()
	var httpServers []*http.Server
	errCh := make(chan error, len(httpEndpoints)+len(tlsEndpoints))
	start := func(label string, endpoints []listenEndpoint, wrap func(net.Listener) net.Listener) {
		for _, endpoint := range endpoints {
			listener, err := net.Listen(endpoint.network, endpoint.address)
			if err != nil {
				log.Printf("listen %s %s: %v", endpoint.network, endpoint.address, err)
				continue
			}
			if wrap != nil {
				listener = wrap(listener)
			}
			httpServer := &http.Server{
				Handler:           handler,
				ReadHeaderTimeout: 10 * time.Second,
				TLSConfig:         tlsConfig,
			}
			httpServers = append(httpServers, httpServer)
			go func(httpServer *http.Server, listener net.Listener, label string) {
				log.Printf("listening on %s (%s)", listener.Addr(), label)
				err := httpServer.Serve(listener)
				if err != nil && !errors.Is(err, http.ErrServerClosed) {
					errCh <- err
				}
			}(httpServer, listener, label)
		}
	}
	start("HTTP", httpEndpoints, nil)
	if tlsConfig != nil {
		start("HTTPS", tlsEndpoints, func(l net.Listener) net.Listener {
			return tls.NewListener(l, tlsConfig)
		})
	}
	if len(httpServers) == 0 {
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	select {
	case err := <-errCh:
		log.Printf("server: %v", err)
		return 1
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		app.Close()
		for _, httpServer := range httpServers {
			if err := httpServer.Shutdown(shutdownCtx); err != nil {
				log.Printf("shutdown: %v", err)
				return 1
			}
		}
	}
	return 0
}

func userCommand(args []string) int {
	if len(args) == 0 {
		usage()
		return 2
	}
	switch args[0] {
	case "upsert":
		return userUpsert(args[1:])
	case "list":
		return userList(args[1:])
	default:
		usage()
		return 2
	}
}

func tlsCommand(args []string) int {
	if len(args) == 0 {
		usage()
		return 2
	}
	switch args[0] {
	case "init":
		return tlsInit(args[1:])
	case "reissue":
		return tlsReissue(args[1:])
	default:
		usage()
		return 2
	}
}

// tlsInit は自家 CA・サーバ証明書・構成プロファイルを生成する。
// 必須ファイルが既にあるときは警告して生成をスキップし、成功時と同じガイダンスを出す。
func tlsInit(args []string) int {
	fs := flag.NewFlagSet("tls init", flag.ContinueOnError)
	configPath := fs.String("config", "config.yaml", "path to config.yaml")
	dirFlag := fs.String("dir", "", "output directory (default: <state_dir>/tls)")
	force := fs.Bool("force", false, "overwrite existing certificate files")
	var hosts multiFlag
	fs.Var(&hosts, "hosts", "DNS name (or IP) for the server certificate SAN; repeat for more")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Printf("load config: %v", err)
		return 1
	}
	dir := *dirFlag
	if dir == "" {
		dir = filepath.Join(cfg.StateDir, "tls")
	} else if !filepath.IsAbs(dir) {
		dir = filepath.Clean(dir)
	}
	paths, skipped, err := localtls.Init(dir, hosts, *force)
	if err != nil {
		log.Printf("tls init: %v", err)
		return 1
	}
	publicHost := ""
	if len(hosts) > 0 {
		publicHost = hosts[0]
	}
	if skipped {
		log.Printf("warning: certificates already exist under %s; skipping generation (use --force to overwrite)", dir)
		if publicHost == "" {
			publicHost, err = localtls.PrimaryDNSName(paths.ServerCert)
			if err != nil {
				log.Printf("tls init: %v", err)
				return 1
			}
		}
		printTLSResult("既存の証明書があります (生成はスキップしました)", paths, publicHost, cfg, configDir(*configPath))
		return 0
	}
	if publicHost == "" {
		log.Printf("at least one --hosts is required")
		return 2
	}
	printTLSResult("証明書を生成しました", paths, publicHost, cfg, configDir(*configPath))
	return 0
}

// tlsReissue は既存 CA のままサーバ証明書だけを作り直す。
func tlsReissue(args []string) int {
	fs := flag.NewFlagSet("tls reissue", flag.ContinueOnError)
	configPath := fs.String("config", "config.yaml", "path to config.yaml")
	dirFlag := fs.String("dir", "", "TLS directory (default: <state_dir>/tls)")
	var hosts multiFlag
	fs.Var(&hosts, "hosts", "DNS name (or IP) for the server certificate SAN; repeat for more")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if len(hosts) == 0 {
		log.Printf("at least one --hosts is required")
		return 2
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Printf("load config: %v", err)
		return 1
	}
	dir := *dirFlag
	if dir == "" {
		dir = filepath.Join(cfg.StateDir, "tls")
	} else if !filepath.IsAbs(dir) {
		dir = filepath.Clean(dir)
	}
	paths, err := localtls.ReissueServer(dir, hosts)
	if err != nil {
		log.Printf("tls reissue: %v", err)
		return 1
	}
	printTLSResult("サーバ証明書を再発行しました (CA はそのまま)", paths, hosts[0], cfg, configDir(*configPath))
	return 0
}

func printTLSResult(title string, paths localtls.Paths, publicHost string, cfg config.Config, configDirectory string) {
	tlsListen := cfg.TLS.Listen
	if tlsListen == "" {
		if derived, err := config.ReplaceListenPort(cfg.Listen, "8788"); err == nil {
			tlsListen = derived
		} else {
			tlsListen = "0.0.0.0:8788"
		}
	}
	httpPort, _ := cfg.ListenPort()
	if httpPort == "" {
		httpPort = "8787"
	}
	_, httpsPort, err := net.SplitHostPort(tlsListen)
	if err != nil || httpsPort == "" {
		httpsPort = "8788"
	}
	fmt.Println(title)
	fmt.Printf("  CA 証明書: %s\n", paths.CACert)
	fmt.Printf("  CA 秘密鍵: %s (配信しない)\n", paths.CAKey)
	fmt.Printf("  サーバ証明書: %s\n", paths.ServerCert)
	fmt.Printf("  サーバ秘密鍵: %s\n", paths.ServerKey)
	fmt.Printf("  構成プロファイル: %s\n", paths.Mobileconfig)
	fmt.Println()
	fmt.Println("config.yaml に次を追加し、ホスト名がサーバ IP に解決されるようにしてから serve してください:")
	fmt.Println("（パスは config.yaml からの相対パス。起動時のカレントディレクトリには依存しません）")
	fmt.Println()
	fmt.Printf("tls:\n")
	fmt.Printf("  listen: %q\n", tlsListen)
	fmt.Printf("  cert_file: %q\n", suggestConfigPath(configDirectory, paths.ServerCert))
	fmt.Printf("  key_file: %q\n", suggestConfigPath(configDirectory, paths.ServerKey))
	fmt.Printf("  ca_file: %q\n", suggestConfigPath(configDirectory, paths.CACert))
	fmt.Printf("  public_host: %q\n", publicHost)
	fmt.Println()
	fmt.Printf("HTTP (セットアップ) は listen (%s)、HTTPS は tls.listen (%s) で待ち受けます。\n", cfg.Listen, tlsListen)
	fmt.Printf("iOS では http://<LAN-IP>:%s/setup を開き、構成プロファイルのインストールと証明書信頼設定を行います。\n", httpPort)
	fmt.Printf("常用 URL は https://%s/ です。\n", net.JoinHostPort(publicHost, httpsPort))
}

// configDir は設定ファイルがあるディレクトリの絶対パスを返す。
func configDir(configPath string) string {
	abs, err := filepath.Abs(configPath)
	if err != nil {
		return filepath.Dir(configPath)
	}
	return filepath.Dir(abs)
}

// suggestConfigPath は設定ファイルディレクトリからの相対パスを返す。
// 相対にできないときだけ絶対パスのまま返す。
func suggestConfigPath(configDirectory, path string) string {
	if configDirectory == "" {
		configDirectory = "."
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		absPath = path
	}
	rel, err := filepath.Rel(configDirectory, absPath)
	if err != nil || rel == "" || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return absPath
	}
	return filepath.ToSlash(rel)
}

func userUpsert(args []string) int {
	fs := flag.NewFlagSet("user upsert", flag.ContinueOnError)
	configPath := fs.String("config", "config.yaml", "path to config.yaml")
	username := fs.String("username", "", "user name")
	clearMACs := fs.Bool("clear-macs", false, "replace MAC addresses with none, or with repeated --mac values")
	var macs multiFlag
	fs.Var(&macs, "mac", "allowed MAC address; repeat for more than one")
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
	secret, err := password.ReadNew(os.Stdin, os.Stderr)
	if err != nil {
		log.Printf("password: %v", err)
		return 1
	}
	file, err := users.Load(cfg.UsersFile)
	if err != nil {
		log.Printf("load users: %v", err)
		return 1
	}
	keepMACs := len(macs) == 0 && !*clearMACs
	if err := file.Upsert(*username, secret, macs, keepMACs, bcrypt.DefaultCost); err != nil {
		log.Printf("upsert user: %v", err)
		return 1
	}
	if err := users.Save(cfg.UsersFile, file); err != nil {
		log.Printf("save users: %v", err)
		return 1
	}
	layout, err := cursor.LayoutFor(cfg.StateDir, *username)
	if err != nil {
		log.Printf("cursor layout: %v", err)
		return 1
	}
	// 切断時の busy/idle 判定用に、CLI の status indicators を有効化する。
	if err := layout.Ensure(); err != nil {
		log.Printf("prepare cursor home: %v", err)
		return 1
	}
	fmt.Printf("ユーザーを更新しました: %s\n", *username)
	return 0
}

func userList(args []string) int {
	fs := flag.NewFlagSet("user list", flag.ContinueOnError)
	configPath := fs.String("config", "config.yaml", "path to config.yaml")
	if err := fs.Parse(args); err != nil {
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
	for _, user := range file.Users {
		fmt.Printf("%s\t%s\n", user.Username, strings.Join(user.MACAddresses, ","))
	}
	return 0
}

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

type multiFlag []string

func (m *multiFlag) String() string {
	return strings.Join(*m, ",")
}

func (m *multiFlag) Set(value string) error {
	*m = append(*m, value)
	return nil
}
