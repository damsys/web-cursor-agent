package main

import (
	"fmt"
	"net"
)

type listenEndpoint struct {
	network string
	address string
}

// listenEndpoints は待受アドレスをアドレスファミリごとのソケットに分ける。
// Go は 0.0.0.0 を IPv6 のワイルドカードにまとめる。WSL2 は Windows の 127.0.0.1 をそのソケットへ転送しない。
func listenEndpoints(addr string) ([]listenEndpoint, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("invalid listen address %q", addr)
	}
	switch host {
	case "", "0.0.0.0", "::":
		return []listenEndpoint{
			{network: "tcp4", address: net.JoinHostPort("0.0.0.0", port)},
			{network: "tcp6", address: net.JoinHostPort("::", port)},
		}, nil
	}
	network := "tcp"
	if ip := net.ParseIP(host); ip != nil {
		if ip.To4() != nil {
			network = "tcp4"
		} else {
			network = "tcp6"
		}
	}
	return []listenEndpoint{{network: network, address: addr}}, nil
}
