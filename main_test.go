package main

import "testing"

func TestProbeAddr(t *testing.T) {
	for listen, want := range map[string]string{
		":8080":          "127.0.0.1:8080",
		"0.0.0.0:8080":   "127.0.0.1:8080",
		"[::]:8080":      "[::1]:8080",
		"127.0.0.1:9000": "127.0.0.1:9000",
		"10.0.0.5:8080":  "10.0.0.5:8080",
		"[::1]:8080":     "[::1]:8080",
	} {
		if got := probeAddr(listen); got != want {
			t.Errorf("probeAddr(%q) = %q, want %q", listen, got, want)
		}
	}
}
