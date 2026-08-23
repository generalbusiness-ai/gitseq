#!/usr/bin/env bash
# The scanner must detect, not merely pass. A gate that is green because it
# looks at nothing is worse than no gate, because it is also reassuring.
#
# Every fixture is assembled at runtime from parts. Writing the shapes
# literally would put five real secret shapes into tracked content, and the
# scanner would then flag its own evidence and fail the repository it is meant
# to protect - which is exactly what happened to an earlier revision of this
# file, caught in review rather than by me.
#
# Detection alone is not enough to assert. The scanner reports into public CI
# logs, so every detect case also asserts that the planted content never
# appears in the scanner's output: a scanner that echoes what it found has
# leaked the secret it was built to contain.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

scanner="$PWD/.github/scripts/scan-secrets.sh"
failures=0
workspace="$(mktemp -d)"
# Scoped cleanup that removes only what this test created, and never with a
# recursive force delete.
cleanup() {
	[ -n "${workspace:-}" ] || return 0
	find "$workspace" -mindepth 1 -depth -exec rm -f {} + 2>/dev/null || true
	find "$workspace" -mindepth 1 -depth -type d -exec rmdir {} + 2>/dev/null || true
	rmdir "$workspace" 2>/dev/null || true
}
trap cleanup EXIT

# Assembled so no literal secret shape appears in this file.
begin="-----BEGIN"; end="-----"
github_token="gh""p_0123456789abcdefghijklmnopqrstuvwxyz"
aws_key="AKI""AIOSFODNN7EXAMPLE"
slack_token="xox""b-000000000000-abcdefghijkl"
github_pat="github""_pat_"$(printf 'A%.0s' $(seq 1 62))
api_secret="sk""-"$(printf 'b%.0s' $(seq 1 40))
openssh_key="$begin OPENSSH PRIVATE KEY$end"
rsa_key="$begin RSA PRIVATE KEY$end"

expect() {
	local name="$1" want="$2" content="$3" got
	local dir="$workspace/case-$RANDOM$RANDOM"
	mkdir -p "$dir"
	git -C "$dir" init -q .
	printf '%s\n' "$content" > "$dir/planted"
	git -C "$dir" add -A
	# Assert on the diagnostic, not merely on a nonzero exit. A scanner that
	# crashed - bad regex, missing git, syntax error - also exits nonzero, and
	# reading that as "detected" would let a broken gate report success at
	# catching things.
	local output status
	output="$(cd "$dir" && "$scanner" 2>&1)" && status=0 || status=$?
	if [ "$status" -eq 0 ]; then
		got=pass
	elif [ "$status" -eq 1 ] && printf '%s' "$output" | grep -q 'committed secret candidate:'; then
		got=detect
	else
		printf 'FAIL %-28s scanner failed without reporting a candidate (status %s): %s\n' "$name" "$status" "$output" >&2
		failures=$((failures + 1))
		return
	fi
	if [ "$got" != "$want" ]; then
		printf 'FAIL %-28s want=%s got=%s\n' "$name" "$want" "$got" >&2
		failures=$((failures + 1))
		return
	fi
	# A detection must name the file so a maintainer can act on it, and must
	# not echo the planted content: this output lands in public CI logs.
	if [ "$got" = detect ]; then
		if printf '%s' "$output" | grep -qF -- "$content"; then
			printf 'FAIL %-28s scanner output echoes the planted content\n' "$name" >&2
			failures=$((failures + 1))
			return
		fi
		if ! printf '%s' "$output" | grep -qF -- 'planted'; then
			printf 'FAIL %-28s scanner output does not name the file\n' "$name" >&2
			failures=$((failures + 1))
			return
		fi
	fi
	printf 'ok   %-28s %s\n' "$name" "$got"
}

expect "github token"        detect "token: $github_token"
expect "aws key id"          detect "id: $aws_key"
expect "openssh private key" detect "$openssh_key"
expect "rsa private key"     detect "$rsa_key"
expect "slack token"         detect "$slack_token"
expect "github fine-grained"  detect "token: $github_pat"
expect "api secret key"       detect "key: $api_secret"

# The false-positive side, which is what makes the gate usable in a repository
# whose history is mostly hex.
expect "event identifier"    pass   "git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:b91d6a7283ced2525be41196f32edc10d3634d75"
expect "commit object id"    pass   "b7f5b01516815963a7bdfbb6671bf06b47bbfa08"
expect "ssh public key"      pass   "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleExampleExampleExampleExampleExa user@host"

# A credential inside a file git classifies as binary. `git grep -I` skips
# such files silently, and a scanner built on it reports a clean tree it
# never looked at - which is worse than no scan, because the green result is
# reassuring. The NUL byte before the token is what flips git's classifier.
nulcase="$workspace/nulcase"
mkdir -p "$nulcase"
git -C "$nulcase" init -q .
printf 'blob\0data token: %s\n' "$github_token" > "$nulcase/planted"
git -C "$nulcase" add -A
nul_output="$(cd "$nulcase" && "$scanner" 2>&1)" && nul_status=0 || nul_status=$?
if [ "$nul_status" -eq 1 ] && printf '%s' "$nul_output" | grep -q 'committed secret candidate:'; then
	printf 'ok   %-28s %s\n' "binary-classified file" "detect"
else
	printf 'FAIL %-28s a credential in a NUL-containing tracked file was missed (status %s)\n' "binary-classified file" "$nul_status" >&2
	failures=$((failures + 1))
fi

# The report must never contain the credential it detected, on stdout or
# stderr. Plant every credential shape at once and require that none of them
# appears anywhere in the combined output.
redact="$workspace/redact"
mkdir -p "$redact"
git -C "$redact" init -q .
{
	printf 'token: %s\n' "$github_token"
	printf 'pat: %s\n' "$github_pat"
	printf 'id: %s\n' "$aws_key"
	printf 'key: %s\n' "$api_secret"
	printf 'slack: %s\n' "$slack_token"
	printf '%s\n' "$openssh_key"
} > "$redact/planted"
git -C "$redact" add -A
redact_output="$(cd "$redact" && "$scanner" 2>&1)" && redact_status=0 || redact_status=$?
redact_ok=1
if [ "$redact_status" -ne 1 ] || ! printf '%s' "$redact_output" | grep -q 'committed secret candidate:'; then
	printf 'FAIL %-28s expected every planted credential to be detected (status %s)\n' "output is redacted" "$redact_status" >&2
	redact_ok=0
fi
for secret in "$github_token" "$github_pat" "$aws_key" "$api_secret" "$slack_token" "$openssh_key"; do
	if printf '%s' "$redact_output" | grep -qF -- "$secret"; then
		printf 'FAIL %-28s a planted credential appears in scanner output\n' "output is redacted" >&2
		redact_ok=0
	fi
done
if [ "$redact_ok" -eq 1 ]; then
	printf 'ok   %-28s %s\n' "output is redacted" "detect"
else
	failures=$((failures + 1))
fi

# Scope, pinned rather than described. The scanner inspects the tracked current
# tree, not history and not the working directory. Both halves matter: a secret
# that is merely lying in a checkout is not what a public repository leaks, and
# a gate that depended on stray working files would pass or fail by accident.
scope="$workspace/scope"
mkdir -p "$scope"
git -C "$scope" init -q .
printf 'token: %s\n' "$github_token" > "$scope/planted"
if (cd "$scope" && "$scanner" >/dev/null 2>&1); then
	printf 'ok   %-28s %s\n' "untracked is ignored" "pass"
else
	printf 'FAIL %-28s an untracked file was scanned\n' "untracked is ignored" >&2
	failures=$((failures + 1))
fi
git -C "$scope" add -A
scope_output="$(cd "$scope" && "$scanner" 2>&1)" && scope_status=0 || scope_status=$?
if [ "$scope_status" -eq 1 ] && printf '%s' "$scope_output" | grep -q 'committed secret candidate:' \
	&& ! printf '%s' "$scope_output" | grep -qF -- "$github_token"; then
	printf 'ok   %-28s %s\n' "tracked is scanned" "detect"
else
	printf 'FAIL %-28s a tracked secret was missed or echoed (status %s)\n' "tracked is scanned" "$scope_status" >&2
	failures=$((failures + 1))
fi

# And the case that matters most: this repository, with this file tracked,
# must pass its own gate - and say so, rather than exiting quietly. An
# earlier revision did not pass.
self_output="$("$scanner" 2>&1)" && self_status=0 || self_status=$?
if [ "$self_status" -eq 0 ] && printf '%s' "$self_output" | grep -q 'No committed-secret candidates'; then
	printf 'ok   %-28s %s\n' "this repository" "pass"
else
	printf 'FAIL %-28s the scanner flags its own repository (status %s)\n' "this repository" "$self_status" >&2
	failures=$((failures + 1))
fi

if [ "$failures" -ne 0 ]; then
	printf '\n%d scanner cases failed\n' "$failures" >&2
	exit 1
fi
printf '\nall scanner cases behaved as required\n'
