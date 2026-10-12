package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"web-cursor-agent/internal/config"
	"web-cursor-agent/internal/server"
)

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
