package service

import (
 "bytes"
 "encoding/json"
 "net/http"
 "net/http/httptest"
 "strings"
 "testing"
)

func TestPlannerAuthorizationRetryThroughListeningHTTPServer(t *testing.T) {
 f := newAuthorizationFixture(t)
 server := httptest.NewServer(f.server.Handler())
 defer server.Close()
 post := func(input actRequest) (string, string) {
  t.Helper()
  data, err := json.Marshal(input); if err != nil { t.Fatal(err) }
  response, err := server.Client().Post(server.URL+"/v0/act", "application/json", bytes.NewReader(data)); if err != nil { t.Fatal(err) }
  defer response.Body.Close()
  var answer struct { ID string `json:"id"`; Error string `json:"error"` }
  if err := json.NewDecoder(response.Body).Decode(&answer); err != nil { t.Fatal(err) }
  if response.StatusCode != http.StatusOK && answer.Error == "" { t.Fatalf("unexplained HTTP %d", response.StatusCode) }
  return answer.ID, answer.Error
 }
 request, errText := post(actRequest{Session:f.credential, Act:"state", Kind:"request", Text:"bounded socket control", Body:map[string]string{"to":"reviewer", "conditions":"socket control"}, IdempotencyKey:"socket-request"})
 if request == "" { t.Fatal(errText) }
 measured := f.git("rev-parse", "HEAD")
 input := actRequest{Session:f.credential, Act:"state", Kind:"report", Text:"socket authorization", Body:map[string]string{"authorizes_request":request, "target_ref":f.ref, "target_pre_head":measured}, RestsOn:[]string{request}, IdempotencyKey:"socket-accepted"}
 original, errText := post(input); if original == "" { t.Fatal(errText) }
 accepted := f.snapshot()
 f.git("commit", "--allow-empty", "-qm", "move after accepted socket report")
 replay, errText := post(input); if replay != original || errText != "" { t.Fatalf("exact retry: %q %q, want %q", replay, errText, original) }
 f.unmoved(accepted, "socket replay")
 changed := input; changed.Text = "different signed intent"
 id, errText := post(changed); if id != "" || errText == "" { t.Fatalf("changed intent accepted: %q %q", id, errText) }
 f.unmoved(accepted, "socket conflicting retry")
 fresh := input; fresh.IdempotencyKey = "socket-fresh"
 id, errText = post(fresh); if id != "" || !strings.Contains(errText, "target_pre_head") { t.Fatalf("fresh stale report: %q %q", id, errText) }
 f.unmoved(accepted, "socket stale refusal")
 missing := input; missing.IdempotencyKey="socket-missing-ref"; missing.Body=map[string]string{"authorizes_request":request,"target_ref":"refs/heads/absent-socket-control","target_pre_head":measured}
 id, errText=post(missing); if id!="" || errText=="" { t.Fatalf("missing ref accepted: %q %q",id,errText) }
 f.unmoved(accepted,"socket missing-ref refusal")
 forged := fresh; forged.Session="credential:invalid-socket-control"
 id, errText=post(forged); if id!="" || !strings.Contains(errText,"credential is not valid") { t.Fatalf("credential boundary: %q %q",id,errText) }
 f.unmoved(accepted,"socket forged credential")
}
