package forge

import "testing"

func TestCodexPortGateAdditionalBoundaries(t *testing.T) {
 cases := []struct { name, text string; unsafe bool }{
  {"malformed target with allowed host", "services:\n  forge:\n    ports:\n      - \"127.0.0.1:3300:not-a-port\"\n", true},
  {"unsupported protocol with allowed host", "services:\n  forge:\n    ports:\n      - \"127.0.0.1:3300:3000/not-a-protocol\"\n", true},
  {"IPv4 range", "services:\n  forge:\n    ports:\n      - \"127.0.0.1:5000-5010:5000-5010\"\n", false},
  {"bracketed IPv6 range", "services:\n  forge:\n    ports:\n      - \"[::1]:5000-5010:5000-5010/udp\"\n", false},
  {"documented unbracketed IPv6 loopback", "services:\n  forge:\n    ports:\n      - \"::1:6000:6000\"\n", false},
  {"second document wildcard port", "services:\n  forge:\n    ports:\n      - \"127.0.0.1:3300:3000\"\n---\nservices:\n  other:\n    image: busybox\n    ports:\n      - \"0.0.0.0:9000:9000\"\n", true},
 }
 for _, c := range cases { t.Run(c.name, func(t *testing.T) {
  got := unsafeBindings(t,c.text)
  if (len(got)>0) != c.unsafe { t.Errorf("unsafe=%v, want %v; findings=%v", len(got)>0,c.unsafe,got) }
 }) }
}
