#!/usr/bin/env bash
# Look for committed secrets in tracked content.
#
# Repository-owned on purpose. This history carries hex event identifiers,
# commit object IDs, SSH public keys and signature blobs by the thousand, and a
# generic entropy scanner reports those endlessly. A gate that is wrong most of
# the time is one people learn to click past, so this matches the shapes that
# are actually secret rather than everything that looks random.
#
# It scans tracked files only: untracked working files are not what a public
# repository leaks, and including them would make the gate depend on whatever
# happens to be lying in a checkout.
#
# Nothing is excluded, not even this file or its test. An exclusion is a hole
# somebody can hide a secret in, and the test assembles its fixtures at runtime
# precisely so no exclusion is needed.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

status=0
report() {
	printf 'committed secret candidate: %s\n' "$1" >&2
	status=1
}

# PEM private key blocks of any flavour, which is the one shape that is never
# a false positive: nothing in this repository legitimately commits one.
while IFS= read -r file; do
	[ -n "$file" ] || continue
	report "$file contains a PEM private key block"
done < <(git grep -lI -E -- '-----BEGIN [A-Z ]*PRIVATE KEY-----' -- . || true)

# Provider-issued credentials with recognisable prefixes. These are matched by
# their issuer's own format rather than by entropy, so a hex event id cannot
# trip them.
patterns=(
	'gh[pousr]_[A-Za-z0-9]{36,}'          # GitHub personal access / OAuth tokens
	'github_pat_[A-Za-z0-9_]{60,}'        # GitHub fine-grained tokens
	'AKIA[0-9A-Z]{16}'                    # AWS access key id
	'sk-[A-Za-z0-9]{32,}'                 # common API secret-key form
	'xox[baprs]-[A-Za-z0-9-]{10,}'        # Slack tokens
)
for pattern in "${patterns[@]}"; do
	while IFS= read -r hit; do
		[ -n "$hit" ] || continue
		report "$hit"
	done < <(git grep -nI -E -- "$pattern" -- . || true)
done

if [ "$status" -ne 0 ]; then
	printf '\nRefusing: tracked content matches a committed-secret shape.\n' >&2
	printf 'If this is a false positive, narrow the pattern rather than deleting the gate.\n' >&2
	exit 1
fi

printf 'No committed-secret candidates in tracked content.\n'
