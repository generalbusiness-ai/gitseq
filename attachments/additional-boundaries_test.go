package forge

import ("testing"; "gopkg.in/yaml.v3")

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

func TestCodexMalformedPortBoundaries(t *testing.T) {
 for _, entry := range []string{"127.0.0.1:3300:", "[::1:3300:3000", "::1]:3300:3000", "[[::1]]:3300:3000"} {
  t.Run(entry, func(t *testing.T) { text := "services:\n  forge:\n    ports:\n      - \"" + entry + "\"\n"; got := unsafeBindings(t,text); if len(got)==0 { t.Errorf("malformed entry cleared: %q",entry) } })
 }
}

func TestCodexDuplicateHostField(t *testing.T) {
 text := "services:\n  forge:\n    ports:\n      - target: 3000\n        published: 3300\n        host_ip: 0.0.0.0\n        host_ip: 127.0.0.1\n"
 var generic any
 if err := yaml.Unmarshal([]byte(text), &generic); err == nil { t.Fatal("control: duplicate-key YAML unexpectedly accepted by ordinary decode") }
 if got := unsafeBindings(t,text); len(got)==0 { t.Fatal("duplicate host_ip cleared although ordinary YAML decode rejects it") }
}
