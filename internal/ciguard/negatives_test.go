package ciguard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A pinned reference for fixtures. The SHA only has to be shaped right; the
// classifier judges form, not reachability.
const pinned = "actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1"

// The semantic negatives. A gate is only worth what it refuses, and a test
// over the real workflows can never show a refusal because the real workflows
// are kept clean. Each case here is a workflow this repository must never
// carry, checked against the same production rule the real walk uses, and each
// asserts the specific reason - a case that merely wants "some violation"
// would keep passing after the rule decayed into a different, weaker rule.
func TestRejectsInadmissibleAutomation(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		// files places local fixtures - action manifests, reusable workflows -
		// under the fixture root before the check runs.
		files map[string]string
		// want lists substrings that must each appear among the violations.
		// Empty means the fixture must pass clean.
		want []string
	}{
		{
			name: "write-all scalar",
			yaml: `
permissions: write-all
jobs:
  build:
    steps:
      - run: make test
`,
			want: []string{`declares permissions as the scalar "write-all"`},
		},
		{
			name: "read-all scalar",
			yaml: `
permissions: read-all
jobs:
  build:
    steps:
      - run: make test
`,
			want: []string{`declares permissions as the scalar "read-all"`},
		},
		{
			name: "permissions none",
			yaml: `
permissions: none
jobs:
  build:
    steps:
      - run: make test
`,
			want: []string{`declares permissions as the scalar "none"`},
		},
		{
			name: "permissions omitted everywhere",
			yaml: `
jobs:
  build:
    steps:
      - run: make test
`,
			want: []string{`declares no permissions at the workflow level and job "build" declares none either`},
		},
		{
			name: "contents omitted",
			yaml: `
permissions:
  actions: read
jobs:
  build:
    steps:
      - run: make test
`,
			want: []string{
				`grants unexpected scope "actions"`,
				`omits the required grant contents: read`,
			},
		},
		{
			name: "contents write",
			yaml: `
permissions:
  contents: write
jobs:
  build:
    steps:
      - run: make test
`,
			want: []string{`grants "contents" level "write"; the allowlist admits only "read"`},
		},
		{
			name: "unexpected scope beside a correct grant",
			yaml: `
permissions:
  contents: read
  id-token: write
jobs:
  build:
    steps:
      - run: make test
`,
			want: []string{`grants unexpected scope "id-token"`},
		},
		{
			name: "job override widens to write",
			yaml: `
permissions:
  contents: read
jobs:
  release:
    permissions:
      contents: write
    steps:
      - run: make build
`,
			want: []string{`job release grants "contents" level "write"; the allowlist admits only "read"`},
		},
		{
			name: "job override is write-all",
			yaml: `
permissions:
  contents: read
jobs:
  release:
    permissions: write-all
    steps:
      - run: make build
`,
			want: []string{`job release declares permissions as the scalar "write-all"`},
		},
		{
			name: "job override adds an unexpected scope",
			yaml: `
permissions:
  contents: read
jobs:
  publish:
    permissions:
      contents: read
      packages: write
    steps:
      - run: make build
`,
			want: []string{`job publish grants unexpected scope "packages"`},
		},
		{
			name: "unpinned step uses",
			yaml: `
permissions:
  contents: read
jobs:
  build:
    steps:
      - uses: actions/checkout@v4
`,
			want: []string{`"actions/checkout@v4" is external and not pinned to a full 40-hex commit SHA`},
		},
		{
			name: "short-sha step uses",
			yaml: `
permissions:
  contents: read
jobs:
  build:
    steps:
      - uses: actions/checkout@3d3c42e
`,
			want: []string{`"actions/checkout@3d3c42e" is external and not pinned to a full 40-hex commit SHA`},
		},
		{
			name: "unpinned job-level uses",
			yaml: `
permissions:
  contents: read
jobs:
  reuse:
    uses: octo-org/workflows/.github/workflows/build.yml@main
`,
			want: []string{`job reuse: "octo-org/workflows/.github/workflows/build.yml@main" is external and not pinned to a full 40-hex commit SHA`},
		},
		{
			name: "docker image step uses",
			yaml: `
permissions:
  contents: read
jobs:
  build:
    steps:
      - uses: docker://alpine:3.20
`,
			want: []string{`"docker://alpine:3.20" is a container image reference, which this gate does not model or pin`},
		},
		{
			name: "job container image",
			yaml: `
permissions:
  contents: read
jobs:
  build:
    container:
      image: node:24
    steps:
      - run: make test
`,
			want: []string{`job build declares a job container image, which this gate does not model or pin`},
		},
		{
			name: "job service images",
			yaml: `
permissions:
  contents: read
jobs:
  build:
    services:
      db:
        image: postgres:16
    steps:
      - run: make test
`,
			want: []string{`job build declares service container images, which this gate does not model or pin`},
		},
		{
			name: "local action without a manifest",
			yaml: `
permissions:
  contents: read
jobs:
  build:
    steps:
      - uses: ./.github/actions/missing
`,
			want: []string{`"./.github/actions/missing" has no readable action manifest`},
		},
		{
			name: "local composite action with an unpinned nested uses",
			yaml: `
permissions:
  contents: read
jobs:
  build:
    steps:
      - uses: ./.github/actions/setup
`,
			files: map[string]string{
				".github/actions/setup/action.yml": `
runs:
  using: composite
  steps:
    - uses: actions/setup-go@v5
    - run: go version
      shell: bash
`,
			},
			want: []string{`"./.github/actions/setup" is a composite action whose step is refused: "actions/setup-go@v5" is external and not pinned`},
		},
		{
			name: "local docker action",
			yaml: `
permissions:
  contents: read
jobs:
  build:
    steps:
      - uses: ./.github/actions/dockerised
`,
			files: map[string]string{
				".github/actions/dockerised/action.yml": `
runs:
  using: docker
  image: Dockerfile
`,
			},
			want: []string{`"./.github/actions/dockerised" runs via "docker", which this gate does not model or pin`},
		},
		{
			name: "local reusable workflow that does not exist",
			yaml: `
permissions:
  contents: read
jobs:
  reuse:
    uses: ./.github/workflows/absent.yml
`,
			want: []string{`"./.github/workflows/absent.yml" names a reusable workflow that does not exist`},
		},

		// The positive shapes, so a refusal cannot come from the gate simply
		// refusing everything.
		{
			name: "exact allowlist with pinned steps",
			yaml: `
permissions:
  contents: read
jobs:
  build:
    steps:
      - uses: ` + pinned + `
      - run: make test
`,
		},
		{
			name: "job-level declarations stand in for a missing workflow block",
			yaml: `
jobs:
  build:
    permissions:
      contents: read
    steps:
      - run: make test
`,
		},
		{
			name: "local reusable workflow and composite action, fully pinned",
			yaml: `
permissions:
  contents: read
jobs:
  reuse:
    uses: ./.github/workflows/shared.yml
  build:
    steps:
      - uses: ./.github/actions/setup
`,
			files: map[string]string{
				".github/workflows/shared.yml": `
permissions:
  contents: read
jobs:
  inner:
    steps:
      - run: make test
`,
				".github/actions/setup/action.yml": `
runs:
  using: composite
  steps:
    - uses: ` + pinned + `
    - run: go version
      shell: bash
`,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for rel, content := range tc.files {
				path := filepath.Join(root, filepath.FromSlash(rel))
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatalf("creating fixture dir: %v", err)
				}
				if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
					t.Fatalf("writing fixture %s: %v", rel, err)
				}
			}
			flow, err := parseWorkflow([]byte(tc.yaml))
			if err != nil {
				t.Fatalf("fixture is not valid YAML: %v", err)
			}
			violations := checkWorkflow(root, "fixture.yml", flow)
			joined := strings.Join(violations, "\n")
			for _, want := range tc.want {
				if !strings.Contains(joined, want) {
					t.Errorf("violations do not state %q; got:\n%s", want, joined)
				}
			}
			if len(tc.want) == 0 && len(violations) != 0 {
				t.Errorf("admissible fixture was refused:\n%s", joined)
			}
		})
	}
}
