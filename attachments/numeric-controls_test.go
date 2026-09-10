package forge
import "testing"

func TestCodexPortNumberBounds(t *testing.T) {
 for _, entry := range []string{"127.0.0.1:65536:3000", "127.0.0.1:3300:65536", "127.0.0.1:3300:3001-3000", "127.0.0.1:999999999999999999999999999999:3000"} {
  t.Run(entry, func(t *testing.T) { text := "services:\n  forge:\n    ports:\n      - \""+entry+"\"\n"; if got:=unsafeBindings(t,text);len(got)==0 {t.Fatalf("invalid numeric port cleared: %q",entry)} })
 }
}

func TestCodexPortRangeBoundaries(t *testing.T) {
 cases:=[]struct{name,text string; bad bool}{
 {"at upper bound", "services:\n  forge:\n    ports:\n      - \"127.0.0.1:65535:65535\"\n",false},
 {"host range single target", "services:\n  forge:\n    ports:\n      - \"127.0.0.1:6000-6005:3000\"\n",false},
 {"unequal two ranges", "services:\n  forge:\n    ports:\n      - \"127.0.0.1:6000-6001:3000-3002\"\n",true},
 {"long target too large", "services:\n  forge:\n    ports:\n      - target: 65536\n        published: 3300\n        host_ip: 127.0.0.1\n",true},
 {"long published reversed", "services:\n  forge:\n    ports:\n      - target: 3000\n        published: 6001-6000\n        host_ip: 127.0.0.1\n",true},
 }
 for _,c:=range cases {t.Run(c.name,func(t *testing.T){ got:=len(unsafeBindings(t,c.text))>0;if got!=c.bad {t.Fatalf("got finding=%v want=%v",got,c.bad)} })}
}
