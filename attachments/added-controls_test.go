package forge
import "testing"

func TestCodexAsymmetricAndPaddedPorts(t *testing.T) {
 cases:=[]struct{name,text string; bad bool}{
 {"single host container range", "services:\n  forge:\n    ports:\n      - \"127.0.0.1:3300:3000-3001\"\n",true},
 {"padded decimal target", "services:\n  forge:\n    ports:\n      - \"127.0.0.1:3300:003000\"\n",false},
 {"padded long published", "services:\n  forge:\n    ports:\n      - target: 3000\n        published: \"003300\"\n        host_ip: 127.0.0.1\n",false},
 }
 for _,c:=range cases {t.Run(c.name,func(t *testing.T){ got:=len(unsafeBindings(t,c.text))>0;if got!=c.bad {t.Fatalf("got finding=%v want=%v",got,c.bad)} })}
}
