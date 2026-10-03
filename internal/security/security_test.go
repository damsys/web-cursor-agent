package security

import (
	"net"
	"testing"
)

func TestContainsLoopbackAndSubnet(t *testing.T) {
	_, local, err := net.ParseCIDR("192.168.1.20/24")
	if err != nil {
		t.Fatal(err)
	}
	_, extra, err := net.ParseCIDR("10.1.0.0/16")
	if err != nil {
		t.Fatal(err)
	}
	if !Contains(net.ParseIP("127.0.0.1"), nil, nil) {
		t.Fatal("loopback should be allowed")
	}
	if !Contains(net.ParseIP("192.168.1.50"), []net.IPNet{*local}, nil) {
		t.Fatal("same subnet should be allowed")
	}
	if Contains(net.ParseIP("192.168.2.50"), []net.IPNet{*local}, nil) {
		t.Fatal("other subnet should be denied")
	}
	if !Contains(net.ParseIP("10.1.2.3"), nil, []*net.IPNet{extra}) {
		t.Fatal("extra cidr should be allowed")
	}
}

func TestParseProcNetARP(t *testing.T) {
	text := `IP address       HW type     Flags       HW address            Mask     Device
192.168.1.10     0x1         0x2         aa:bb:cc:dd:ee:ff     *        eth0
192.168.1.11     0x1         0x0         00:00:00:00:00:00     *        eth0
`
	mac, ok := ParseProcNetARP(text, net.ParseIP("192.168.1.10"))
	if !ok || mac != "aa:bb:cc:dd:ee:ff" {
		t.Fatalf("mac = %q ok=%v", mac, ok)
	}
	if _, ok := ParseProcNetARP(text, net.ParseIP("192.168.1.11")); ok {
		t.Fatal("incomplete arp entry should be ignored")
	}
}

func TestParseIPNeigh(t *testing.T) {
	text := "192.168.1.5 dev eth0 lladdr AA-BB-CC-DD-EE-01 REACHABLE\n"
	mac, ok := ParseIPNeigh(text, net.ParseIP("192.168.1.5"))
	if !ok || mac != "aa:bb:cc:dd:ee:01" {
		t.Fatalf("mac = %q ok=%v", mac, ok)
	}
}

func TestCheckOrigin(t *testing.T) {
	if !CheckOrigin("GET", "/", "", "localhost:8787") {
		t.Fatal("document GET may omit origin")
	}
	if CheckOrigin("POST", "/api/login", "", "localhost:8787") {
		t.Fatal("POST requires origin")
	}
	if !CheckOrigin("POST", "/api/login", "http://localhost:8787", "localhost:8787") {
		t.Fatal("matching origin should pass")
	}
	if CheckOrigin("GET", "/ws/terminal", "http://evil.example", "localhost:8787") {
		t.Fatal("websocket origin must match")
	}
}

func TestNormalizeMACList(t *testing.T) {
	got, err := NormalizeMACList([]string{"AA:BB:CC:DD:EE:FF", "aa-bb-cc-dd-ee-ff", ""})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "aa:bb:cc:dd:ee:ff" {
		t.Fatalf("got %#v", got)
	}
}
