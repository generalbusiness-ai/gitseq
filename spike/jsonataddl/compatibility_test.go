package jsonataddl

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/generalbusiness-ai/gitseq/host"
	jsonata "github.com/jsonata-go/jsonata/v206"
)

type compatibilityCorpus struct {
	Reference            string              `json:"reference"`
	Port                 string              `json:"port"`
	ReferenceDivergences string              `json:"reference_divergences"`
	Cases                []compatibilityCase `json:"cases"`
}

type compatibilityCase struct {
	Name       string `json:"name"`
	Expression string `json:"expression"`
	Input      any    `json:"input"`
	Reference  any    `json:"reference"`
	Class      string `json:"class"`
	Reason     string `json:"reason"`
}

func TestJSONataCompatibilityAndDeterminismCorpus(t *testing.T) {
	source, err := os.ReadFile("testdata/compatibility.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus compatibilityCorpus
	if err := json.Unmarshal(source, &corpus); err != nil {
		t.Fatal(err)
	}
	if corpus.Reference != "jsonata-js 2.0.6" || !strings.Contains(corpus.Port, "599f35f32e5f") {
		t.Fatalf("corpus is not bound to the profile's reference and port: %#v", corpus)
	}
	if len(corpus.Cases) < 8 {
		t.Fatalf("corpus has %d cases; it is too small to exercise the profile", len(corpus.Cases))
	}
	documentation, err := os.ReadFile("CORPUS.md")
	if err != nil {
		t.Fatal(err)
	}
	documented := strings.Join(strings.Fields(string(documentation)), " ")
	declared := strings.Join(strings.Fields(corpus.ReferenceDivergences), " ")
	if declared == "" || !strings.Contains(documented, declared) {
		t.Fatalf("CORPUS.md and compatibility.json disagree about reference divergences: %q", corpus.ReferenceDivergences)
	}
	referenceResults := evaluateReferenceCorpus(t, source)

	seenExceptional := map[string]bool{}
	for _, test := range corpus.Cases {
		t.Run(test.Name, func(t *testing.T) {
			switch test.Class {
			case "portable", "order-dependent":
				reference, exists := referenceResults[test.Name]
				if !exists || !reflect.DeepEqual(reference, test.Reference) {
					t.Fatalf("checked reference result changed: live %#v, fixture %#v", reference, test.Reference)
				}
				got := evaluateCorpusExpression(t, test.Expression, test.Input)
				switch test.Class {
				case "portable":
					for run := 0; run < 16; run++ {
						if replay := evaluateCorpusExpression(t, test.Expression, test.Input); !reflect.DeepEqual(replay, got) {
							t.Fatalf("Go result changed between identical runs: first %#v, replay %#v", got, replay)
						}
					}
					if !reflect.DeepEqual(got, test.Reference) {
						t.Fatalf("result differs from %s: Go %#v, reference %#v", corpus.Reference, got, test.Reference)
					}
				case "order-dependent":
					seenExceptional[test.Class] = true
					if test.Reason == "" {
						t.Fatal("order-dependent expression has no reason")
					}
					distinct := map[string]bool{canonicalJSON(t, got): true}
					for run := 0; run < 63; run++ {
						distinct[canonicalJSON(t, evaluateCorpusExpression(t, test.Expression, test.Input))] = true
					}
					if len(distinct) < 2 {
						t.Fatalf("order-sensitive comparison no longer exposes the port's map iteration: %#v", got)
					}
					for encoded := range distinct {
						var result any
						if err := json.Unmarshal([]byte(encoded), &result); err != nil || !sameArrayElements(result, test.Reference) {
							t.Fatalf("order-dependent result changed values: Go %s, reference %#v", encoded, test.Reference)
						}
					}
					files := oneFoldFiles(test.Expression)
					if _, err := Load(files, "app", host.Application{Name: "corpus", FoldVersion: "corpus@0"}); err == nil || !strings.Contains(err.Error(), "order-dependent") {
						t.Fatalf("profile admitted order-dependent expression %q: %v", test.Expression, err)
					}
				}
			case "environment-dependent":
				seenExceptional[test.Class] = true
				if test.Reason == "" {
					t.Fatal("environment-dependent expression has no reason")
				}
				files := oneFoldFiles(test.Expression)
				if _, err := Load(files, "app", host.Application{Name: "corpus", FoldVersion: "corpus@0"}); err == nil || !strings.Contains(err.Error(), "ambient") {
					t.Fatalf("profile admitted environment-dependent expression %q: %v", test.Expression, err)
				}
			default:
				t.Fatalf("unknown compatibility class %q", test.Class)
			}
		})
	}
	for _, class := range []string{"order-dependent", "environment-dependent"} {
		if !seenExceptional[class] {
			t.Errorf("corpus names no %s expression", class)
		}
	}
}

func evaluateReferenceCorpus(t *testing.T, corpus []byte) map[string]any {
	t.Helper()
	module := exec.Command("go", "list", "-m", "-f={{.Dir}}", "github.com/jsonata-go/jsonata")
	directory, err := module.Output()
	if err != nil {
		t.Fatalf("locate pinned JSONata module: %v", err)
	}
	command := exec.Command("node", filepath.Join("testdata", "reference.js"), strings.TrimSpace(string(directory)))
	command.Stdin = bytes.NewReader(corpus)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("run JSONata 2.0.6 reference corpus: %v: %s", err, output)
	}
	var results map[string]any
	if err := json.Unmarshal(output, &results); err != nil {
		t.Fatalf("decode reference corpus output %q: %v", output, err)
	}
	return results
}

func evaluateCorpusExpression(t *testing.T, expression string, input any) any {
	t.Helper()
	program, err := jsonata.Compile(expression, false)
	if err != nil {
		t.Fatal(err)
	}
	program.SetMaxDepth(64)
	program.SetMaxRange(4096)
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	result, err := program.Evaluate(encoded, nil)
	if err != nil {
		t.Fatal(err)
	}
	var decoded any
	if err := json.Unmarshal(result, &decoded); err != nil {
		t.Fatalf("decode JSONata result %q: %v", result, err)
	}
	return decoded
}

func canonicalJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func sameArrayElements(left, right any) bool {
	leftArray, leftOK := left.([]any)
	rightArray, rightOK := right.([]any)
	if !leftOK || !rightOK || len(leftArray) != len(rightArray) {
		return false
	}
	leftJSON := make([]string, len(leftArray))
	rightJSON := make([]string, len(rightArray))
	for index := range leftArray {
		left, leftErr := json.Marshal(leftArray[index])
		right, rightErr := json.Marshal(rightArray[index])
		if leftErr != nil || rightErr != nil {
			return false
		}
		leftJSON[index], rightJSON[index] = string(left), string(right)
	}
	sort.Strings(leftJSON)
	sort.Strings(rightJSON)
	return reflect.DeepEqual(leftJSON, rightJSON)
}

func oneFoldFiles(expression string) fstest.MapFS {
	return fstest.MapFS{
		"app/application.sql": {Data: []byte("CREATE EVENT ping (id TEXT NOT NULL); CREATE TABLE pings (id TEXT PRIMARY KEY); CREATE FOLD ping ON ping READ old OPTIONAL ONE AS SELECT id FROM pings WHERE id = :event.id USING 'fold.jsonata' WRITES pings;")},
		"app/fold.jsonata":    {Data: []byte(expression)},
	}
}
