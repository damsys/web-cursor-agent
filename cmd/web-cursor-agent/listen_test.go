package main

import "testing"

func TestListenEndpointsSplitsWildcard(t *testing.T) {
	got, err := listenEndpoints("0.0.0.0:8787")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].network != "tcp4" || got[0].address != "0.0.0.0:8787" || got[1].network != "tcp6" || got[1].address != "[::]:8787" {
		t.Fatalf("endpoints = %#v", got)
	}
}

func TestListenEndpointsKeepsSpecificIPv4(t *testing.T) {
	got, err := listenEndpoints("127.0.0.1:8787")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].network != "tcp4" || got[0].address != "127.0.0.1:8787" {
		t.Fatalf("endpoints = %#v", got)
	}
}
