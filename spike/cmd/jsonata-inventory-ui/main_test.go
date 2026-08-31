package main

import "testing"

func TestRequireLoopback(t *testing.T) {
	for _, address := range []string{"127.0.0.1:7788", "[::1]:7788", "localhost:7788"} {
		if err := requireLoopback(address); err != nil {
			t.Errorf("requireLoopback(%q): %v", address, err)
		}
	}
	for _, address := range []string{"0.0.0.0:7788", "192.0.2.1:7788", ":7788"} {
		if err := requireLoopback(address); err == nil {
			t.Errorf("requireLoopback(%q) succeeded", address)
		}
	}
}
