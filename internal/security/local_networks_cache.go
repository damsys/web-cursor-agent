package security

import (
	"net"
	"sync"
	"time"
)

// インターフェース一覧は全リクエストで取る必要がないので、短時間キャッシュする。
const localNetworksTTL = 30 * time.Second

var (
	localNetworksMu    sync.Mutex
	localNetworksCache []net.IPNet
	localNetworksAt    time.Time
)

// cachedLocalNetworks は TTL 内なら前回の LocalNetworks 結果を返す。
func cachedLocalNetworks() ([]net.IPNet, error) {
	localNetworksMu.Lock()
	defer localNetworksMu.Unlock()
	if localNetworksCache != nil && time.Since(localNetworksAt) < localNetworksTTL {
		return cloneIPNets(localNetworksCache), nil
	}
	networks, err := listLocalNetworks()
	if err != nil {
		return nil, err
	}
	localNetworksCache = networks
	localNetworksAt = time.Now()
	return cloneIPNets(networks), nil
}

func cloneIPNets(in []net.IPNet) []net.IPNet {
	if in == nil {
		return nil
	}
	out := make([]net.IPNet, len(in))
	for i := range in {
		out[i] = net.IPNet{
			IP:   append(net.IP(nil), in[i].IP...),
			Mask: append(net.IPMask(nil), in[i].Mask...),
		}
	}
	return out
}

// ResetLocalNetworksCache はテストでキャッシュを捨てる。
func ResetLocalNetworksCache() {
	localNetworksMu.Lock()
	defer localNetworksMu.Unlock()
	localNetworksCache = nil
	localNetworksAt = time.Time{}
}
