package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"path/filepath"
	"strings"

	"web-cursor-agent/internal/config"
	"web-cursor-agent/internal/localtls"
)

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
