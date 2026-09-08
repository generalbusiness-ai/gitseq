#!/bin/sh
# Release build: stamps the sanitized GitHub origin into the binary's build
# provenance so serverInfo.websiteUrl reflects the real remote available at
# build time. A repository without a GitHub origin builds unstamped and the
# binary falls back to module-path derivation (see internal/buildinfo).
#
# Usage: scripts/build.sh [OUTPUT]   (default OUTPUT: ./tailapp)
# Set TAILAPPS_VERSION to a validated, unprefixed semantic version only in a
# release build. It is stamped into the binary so MCP and engine.json expose
# the release identity rather than a checkout revision.
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
output="${1:-$repo_root/tailapp}"

remote=$(git -C "$repo_root" remote get-url origin 2>/dev/null || true)
stamp=$("$repo_root/scripts/sanitize-remote.sh" "$remote" 2>/dev/null || true)

flags=""
if [ -n "$stamp" ]; then
  flags="-X github.com/generalbusiness-ai/tailapps/internal/buildinfo.stampedSourceURL=$stamp"
fi

version="${TAILAPPS_VERSION:-}"
if [ -n "$version" ]; then
  valid_version='^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$'
  if ! printf '%s\n' "$version" | grep -Eq "$valid_version"; then
    echo "TAILAPPS_VERSION must be an unprefixed semantic version" >&2
    exit 64
  fi
  version_flag="-X github.com/generalbusiness-ai/tailapps/internal/buildinfo.stampedVersion=$version"
  flags="${flags:+$flags }$version_flag"
fi

cd "$repo_root"
# Release binaries use the public dependency graph, never the local workspace.
GOWORK=off go build -mod=readonly -ldflags "$flags" -o "$output" ./cmd/tailapp
