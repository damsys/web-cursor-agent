package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"web-cursor-agent/internal/config"
	"web-cursor-agent/internal/cursor"
	"web-cursor-agent/internal/password"
	"web-cursor-agent/internal/users"
)

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
