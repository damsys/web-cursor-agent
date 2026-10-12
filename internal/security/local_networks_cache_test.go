package security

import "testing"

func TestLocalNetworksCacheReusesResult(t *testing.T) {
	ResetLocalNetworksCache()
	first, err := LocalNetworks()
	if err != nil {
		t.Fatal(err)
	}
	second, err := LocalNetworks()
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != len(second) {
		t.Fatalf("len first=%d second=%d", len(first), len(second))
	}
	for i := range first {
		if first[i].String() != second[i].String() {
			t.Fatalf("network %d: %s vs %s", i, first[i].String(), second[i].String())
		}
	}
	// 呼び出し側へ返したスライスはキャッシュと独立している。
	if len(first) == 0 {
		return
	}
	original := first[0].String()
	first[0].IP[0] ^= 0xff
	third, err := LocalNetworks()
	if err != nil {
		t.Fatal(err)
	}
	if third[0].String() != original {
		t.Fatalf("cached networks must be cloned; got %s want %s", third[0].String(), original)
	}
	ResetLocalNetworksCache()
}
