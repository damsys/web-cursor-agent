// Package security は接続元のネットワーク位置、MAC アドレス、Origin を判定する。
package security

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Contains は IP が loopback、追加 CIDR、またはローカルインターフェースのサブネットに含まれるかを返す。
func Contains(ip net.IP, local []net.IPNet, extra []*net.IPNet) bool {
	ip = canonicalIP(ip)
	if ip == nil {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	for _, network := range extra {
		if network.Contains(ip) {
			return true
		}
	}
	for _, network := range local {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// LocalNetworks は LAN 判定に使うインターフェースのサブネットを返す。
// コンテナ用のブリッジは、家庭内 LAN のセグメントと混ぜない。
func LocalNetworks() ([]net.IPNet, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("list interfaces: %w", err)
	}
	var networks []net.IPNet
	for _, iface := range ifaces {
		if ignoredInterface(iface.Name) {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			return nil, fmt.Errorf("list addresses for %s: %w", iface.Name, err)
		}
		for _, addr := range addrs {
			network, ok := addr.(*net.IPNet)
			if !ok || network.IP.IsLoopback() {
				continue
			}
			ip := canonicalIP(network.IP)
			if ip == nil || ip.IsLinkLocalUnicast() {
				continue
			}
			mask := network.Mask
			if ip4 := ip.To4(); ip4 != nil {
				ip = ip4
				if len(mask) == 16 {
					mask = mask[12:]
				}
			}
			networks = append(networks, net.IPNet{IP: ip.Mask(mask), Mask: mask})
		}
	}
	return networks, nil
}

// SameSegment は接続元がサーバと同じセグメント、または追加 CIDR に含まれるかを返す。
func SameSegment(ip net.IP, extra []*net.IPNet) (bool, error) {
	local, err := LocalNetworks()
	if err != nil {
		return false, err
	}
	return Contains(ip, local, extra), nil
}

// ParseRemoteAddr は net/http の RemoteAddr から IP を取り出す。
func ParseRemoteAddr(remote string) net.IP {
	host := remote
	if split, _, err := net.SplitHostPort(remote); err == nil {
		host = split
	}
	if zone := strings.IndexByte(host, '%'); zone >= 0 {
		host = host[:zone]
	}
	return net.ParseIP(host)
}

// CheckOrigin は状態変更と WebSocket が同一オリジンから来ていることを確認する。
// 通常の GET は画面遷移のため Origin が無くても受け付ける。
func CheckOrigin(method, path, origin, host string) bool {
	needs := method != "GET" && method != "HEAD" || path == "/ws/terminal"
	if origin == "" {
		return !needs
	}
	return origin == "http://"+host || origin == "https://"+host
}

// NormalizeMAC は MAC アドレスを小文字のコロン区切りに揃える。
func NormalizeMAC(value string) (string, error) {
	parsed, err := net.ParseMAC(strings.TrimSpace(value))
	if err != nil {
		return "", fmt.Errorf("invalid MAC address %q", value)
	}
	return parsed.String(), nil
}

// NormalizeMACList は重複を除いて正規化する。
func NormalizeMACList(values []string) ([]string, error) {
	seen := map[string]struct{}{}
	var out []string
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		normalized, err := NormalizeMAC(value)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	return out, nil
}

// LookupMAC は同一 L2 上の隣接機器から IP に対応する MAC アドレスを探す。
func LookupMAC(ip net.IP) (string, bool) {
	ip = canonicalIP(ip)
	if ip == nil || ip.IsLoopback() {
		return "", false
	}
	if mac, ok := lookupProcNetARP(ip); ok {
		return mac, true
	}
	return lookupIPNeigh(ip)
}

// ParseProcNetARP は /proc/net/arp 形式のテキストから IP の MAC アドレスを読む。
func ParseProcNetARP(text string, ip net.IP) (string, bool) {
	ip = canonicalIP(ip)
	if ip == nil {
		return "", false
	}
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 4 || fields[0] == "IP" {
			continue
		}
		if !ip.Equal(canonicalIP(net.ParseIP(fields[0]))) {
			continue
		}
		if fields[2] == "0x0" {
			continue
		}
		mac, err := NormalizeMAC(fields[3])
		if err != nil || mac == "00:00:00:00:00:00" {
			continue
		}
		return mac, true
	}
	return "", false
}

// ParseIPNeigh は `ip neigh` の出力から MAC アドレスを読む。
func ParseIPNeigh(text string, ip net.IP) (string, bool) {
	ip = canonicalIP(ip)
	if ip == nil {
		return "", false
	}
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 || !ip.Equal(canonicalIP(net.ParseIP(fields[0]))) {
			continue
		}
		for i := 0; i < len(fields)-1; i++ {
			if fields[i] != "lladdr" {
				continue
			}
			mac, err := NormalizeMAC(fields[i+1])
			if err != nil || mac == "00:00:00:00:00:00" {
				continue
			}
			return mac, true
		}
	}
	return "", false
}

func lookupProcNetARP(ip net.IP) (string, bool) {
	raw, err := os.ReadFile("/proc/net/arp")
	if err != nil {
		return "", false
	}
	return ParseProcNetARP(string(raw), ip)
}

func lookupIPNeigh(ip net.IP) (string, bool) {
	cmd := exec.Command("ip", "neigh", "show", ip.String())
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return ParseIPNeigh(string(out), ip)
}

func ignoredInterface(name string) bool {
	switch {
	case name == "docker0" || name == "podman0":
		return true
	case strings.HasPrefix(name, "br-"),
		strings.HasPrefix(name, "veth"),
		strings.HasPrefix(name, "cni"),
		strings.HasPrefix(name, "flannel"):
		return true
	default:
		return false
	}
}

func canonicalIP(ip net.IP) net.IP {
	if ip == nil {
		return nil
	}
	if ip4 := ip.To4(); ip4 != nil {
		return ip4
	}
	return ip
}
