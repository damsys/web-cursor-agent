package main

import (
	"fmt"
	"log"
	"os"
	"strings"
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

type multiFlag []string

func (m *multiFlag) String() string {
	return strings.Join(*m, ",")
}

func (m *multiFlag) Set(value string) error {
	*m = append(*m, value)
	return nil
}
