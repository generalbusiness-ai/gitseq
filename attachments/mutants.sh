#!/bin/bash
# Mutation runner for #20424 (source preview windows). Run from the worktree
# root at the reviewed head with a clean tree; each mutant is applied, the
# named tests run, and the tree restored with git checkout. Expected: every
# mutant FAILS its tests.
set -u
GO() { echo "== go mutant: $1"; go test -count=1 ./internal/gitstore ./internal/service -run 'TestPreview' 2>&1 | grep -E '^(ok|--- FAIL)'; git checkout -- internal; }
UI() { echo "== ui mutant: $1"; (cd ui && node --test test/preview-window.test.mjs test/addressing.test.mjs 2>&1 | grep -E '^(✖|ℹ (pass|fail))'); git checkout -- ui/src; }
W=internal/service/previewwindow.go; P=internal/service/preview.go; T=ui/src/components/Preview.tsx
# Go 1: window chosen by aligned start only (ignores partition)
python3 -c "import re;p='$W';s=open(p).read();s=s.replace('return starts[i] > target','return starts[i] > (target-1)/PreviewWindowLines*PreviewWindowLines+1');open(p,'w').write(s)"; GO window-by-aligned-start-only
# Go 2: serve the neighbouring revision
sed -i '' 's|Store.Preview(ctx, config.ObjectFormat, chosen, input.Path)|Store.Preview(ctx, config.ObjectFormat, result.Heads[0], input.Path)|' $P; GO wrong-revision
# Go 3: partition ignores the byte bound
sed -i '' 's/if count > 0 \&\& (count == PreviewWindowLines || used+size > PreviewWindowBytes) {/if count > 0 \&\& count == PreviewWindowLines {/' $W; GO partition-ignores-byte-bound
# Go 4: no line cut
sed -i '' 's|return cutUTF8(line, PreviewLineBytes)|return line|' $W; GO no-line-cut
# Go 5: raised read cap (literal fixtures must fail)
sed -i '' 's|PreviewReadBudget = 4 << 20|PreviewReadBudget = 8 << 20|' internal/gitstore/preview.go; GO raised-read-cap
# Go 6: small file ignores an out-of-range start
python3 - <<'PY'
p='internal/service/previewwindow.go'; s=open(p).read()
i=s.index('\t\tif start > total {\n\t\t\treturn "", previewWindow{Start: start, End: start - 1, Total: total, Partial: true, Previous: min(1, total)}')
j=s.index('\t\twindow := previewWindow{Start: min(1, total), End: total, Total: total}')
open(p,'w').write(s[:i]+s[j:])
PY
GO small-file-ignores-start
# Go 7: truncation names lines the window does not carry
python3 -c "p='$W';s=open(p).read();s=s.replace('\t\tif len(lines[i]) > PreviewLineBytes {\n\t\t\twindow.Truncated = append(window.Truncated, i+1)\n\t\t}\n','');s=s.replace('\twindow := previewWindow{Start: first, End: last, Total: total, Partial: true}\n','\twindow := previewWindow{Start: first, End: last, Total: total, Partial: true}\n\tfor i := first - 1; i < len(lines) && i < first-1+PreviewWindowLines; i++ {\n\t\tif len(lines[i]) > PreviewLineBytes {\n\t\t\twindow.Truncated = append(window.Truncated, i+1)\n\t\t}\n\t}\n');open(p,'w').write(s)"; GO truncation-names-unreturned-lines
# Go 8: Previous by arithmetic
sed -i '' 's/window.Previous = starts\[index-1\]/window.Previous = max(1, first-PreviewWindowLines)/' $W; GO previous-by-arithmetic
# Go 9: ignore the cited line entirely
python3 -c "p='$W';s=open(p).read();s=s.replace('\tcase line > 0:\n\t\ttarget = line\n','\tcase line > 0:\n\t\ttarget = 1\n');open(p,'w').write(s)"; GO ignore-cited-line
# Go 10: no out-of-range message for a cited line
python3 -c "p='$W';s=open(p).read();s=s.replace('message = fmt.Sprintf(\"Cited line %d is outside this file, which has %d lines.\", line, total)\n\t\t\ttarget = total','target = total');open(p,'w').write(s)"; GO no-out-of-range-message
# Go 11: window count bound dropped (partition by bytes only)
sed -i '' 's/if count > 0 \&\& (count == PreviewWindowLines || used+size > PreviewWindowBytes) {/if count > 0 \&\& used+size > PreviewWindowBytes {/' $W; GO partition-ignores-line-bound
# Go 12: invalid UTF-8 served as text
sed -i '' 's|if !utf8.Valid(object.Content) \|\| bytes.IndexByte(object.Content, 0) >= 0 {|if !utf8.Valid(object.Content[:0]) \|\| bytes.IndexByte(object.Content, 0) >= 0 {|' $P; GO invalid-utf8-served
# Go 13: line/start validation dropped
python3 -c "p='$P';s=open(p).read();s=s.replace('if input.Line < 0 || input.Line > previewLineCeiling || input.Start < 0 || input.Start > previewLineCeiling || (input.Line > 0 || input.Start > 0) && input.Path == \"\" && input.Attachment == \"\" {','if false {');open(p,'w').write(s)"; GO no-line-validation
# UI 1: numbering from one
sed -i '' 's|const first = window?.start ?? 1;|const first = 1;|' $T; UI numbering-ignores-window
# UI 2: request drops line and start
python3 -c "p='$T';s=open(p).read();s=s.replace('api.preview(target, abort.signal)','api.preview({ event: target.event, path: target.path, commit: target.commit, attachment: target.attachment }, abort.signal)');open(p,'w').write(s)"; UI request-drops-line-and-start
# UI 3: stale answer accepted (both defences removed)
python3 -c "p='$T';s=open(p).read();s=s.replace('const result = loaded?.key === requestKey ? loaded.value : undefined;','const result = loaded?.value;');s=s.replace('api.preview(target, abort.signal).then((value) => { if (!abort.signal.aborted) setLoaded','api.preview(target, undefined).then((value) => { if (true) setLoaded');open(p,'w').write(s)"; UI stale-answer-accepted
# UI 4: empty file renders a row / UI 5: phantom trailing row / UI 6: blank line treated as empty
sed -i '' 's/window.end < window.start || window.total === 0 ? \[\]/window.end < window.start ? []/' $T; UI empty-file-renders-a-row
python3 -c "p='$T';s=open(p).read();s=s.replace('(whole ? content.replace(/\\\\n\$/, \"\") : content).split(\"\\\\n\")','content.split(\"\\\\n\")',1);open(p,'w').write(s)"; UI trailing-newline-phantom-row
sed -i '' 's/window.end < window.start || window.total === 0 ? \[\]/window.end < window.start || content === "" ? []/' $T; UI blank-line-treated-as-empty
# UI 7: Next replaces instead of pushing history
python3 -c "p='$T';s=open(p).read();s=s.replace('event.preventDefault(); context.open(target); }','event.preventDefault(); window.history.replaceState({}, \"\", formatAddress({ ...context.address, preview: target })); context.open(target); }',1);open(p,'w').write(s)"; UI next-replaces-instead-of-pushing
git status --porcelain
