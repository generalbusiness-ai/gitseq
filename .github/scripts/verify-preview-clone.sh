#!/bin/sh
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
fixture="$root/.tmp/public-preview-clone"
origin="$fixture/origin.git"
checkout="$fixture/checkout"
head=$(git -C "$root" rev-parse HEAD)

rm -rf "$fixture"
mkdir -p "$fixture"

# Model the first-push boundary with a local bare repository: one branch, no
# tags, task branches, workroom refs, or sequence refs.
git init --bare --quiet "$origin"
git -C "$root" push --quiet "$origin" "$head:refs/heads/main"
git --git-dir="$origin" symbolic-ref HEAD refs/heads/main

refs=$(git --git-dir="$origin" for-each-ref --format='%(refname)')
test "$refs" = "refs/heads/main"

git clone --quiet "$origin" "$checkout"
test "$(git -C "$checkout" rev-parse HEAD)" = "$head"
test "$(git -C "$checkout" symbolic-ref --short HEAD)" = main

# Follow the documented install gate using only what the fresh clone carries.
make -C "$checkout" test vet build
test -z "$(git -C "$checkout" status --porcelain --untracked-files=all)"
