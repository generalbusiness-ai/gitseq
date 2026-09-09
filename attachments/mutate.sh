set -o pipefail
SCRATCH=/private/tmp/claude-501/-Users-hughpyle-play-gitseq/edad3ec5-3d64-4212-89b8-59e5bafff100/scratchpad
UI="$SCRATCH/mutant/ui"
TB="$UI/src/components/TopBar.tsx"
MEM="$UI/src/lib/memory.ts"
OUT="$SCRATCH/mutant-out"
mkdir -p "$OUT"

revert() {
  cp "$SCRATCH/pristine-TopBar.tsx" "$TB"
  cp "$SCRATCH/pristine-memory.ts" "$MEM"
}

run() { # $1 = label
  ( cd "$UI" && node --test test/notifications.test.mjs ) > "$OUT/$1.txt" 2>&1
  echo "$1 rc=$?"
}

revert
run baseline

# 1: mount load omitted
revert
perl -0pi -e 's/    setRead\(loadForYouRead\(genesis, myFingerprint\)\);/    setRead(NOTHING_READ);/' "$TB"
grep -n "setRead(NOTHING_READ);" "$TB" | head -3
run mutation-1-mount-load

# 2: event save omitted
revert
perl -0pi -e 's/    saveForYouRead\(genesis, myFingerprint, next\);/    \/* probe: storage save omitted *\//' "$TB"
grep -n "storage save omitted" "$TB"
run mutation-2-event-save

# 3: actor scoping omitted (key drops the fingerprint)
revert
perl -0pi -e 's/  return `workroom\.foryou\.\$\{genesis\}\.\$\{fingerprint\}`;/  return `workroom.foryou.\${genesis}`;/' "$MEM"
grep -n "return \`workroom.foryou" "$MEM"
run mutation-3-actor-scope

# 4: room scoping omitted (key drops the genesis)
revert
perl -0pi -e 's/  return `workroom\.foryou\.\$\{genesis\}\.\$\{fingerprint\}`;/  return `workroom.foryou.\${fingerprint}`;/' "$MEM"
grep -n "return \`workroom.foryou" "$MEM"
run mutation-4-room-scope

revert
grep -n "return \`workroom.foryou" "$MEM"
grep -n "loadForYouRead(genesis\|saveForYouRead(genesis" "$TB"
