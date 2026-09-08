#!/usr/bin/env python3
"""Read-only actual-source child-spread counter. Usage: python3 replay.py CHECKOUT."""
from pathlib import Path
import subprocess, sys, tempfile
checkout=Path(sys.argv[1]).resolve()
source=checkout/'ui/src/lib/threads.ts'
original=source.read_bytes()
needle='replyChildren.set(parent, [...(replyChildren.get(parent) ?? []), event]);'
text=original.decode()
assert text.count(needle)==1, 'Source changed; reconcile the counter at the actual child-list construction first.'
out=Path(tempfile.mkdtemp(prefix='gitseq-thread-copy-proof-'))
(out/'instrumented.ts').write_text(text.replace(needle,'replyChildren.set(parent, [...globalThis.__countChildCopies(replyChildren.get(parent) ?? []), event]);'))
(out/'probe.mjs').write_text("import assert from 'node:assert/strict';\nconst { buildThreadIndex: original } = await import(process.argv[2]);\nimport { buildThreadIndex as measured } from './instrumented.ts';\nlet copies=0;\nglobalThis.__countChildCopies = function*(items) { for (const item of items) { copies++; yield item; } };\nconst rows=[];\nfor (const count of [1000,2000,4000]) {\n const statements=Array.from({length:count+1},(_,i)=>({event:i?'child-'+i:'root',actor:'actor',kind:'assert',text:'fixture',sequence:i+1}));\n const provenance=Object.fromEntries(statements.slice(1).map(s=>[s.event,['root']]));\n const projection={statements,acts:[],commitments:[],decisions:statements.map(s=>({event:s.event,sequence:s.sequence,verdict:'effective'})),provenance};\n const control=original(projection); copies=0; const result=measured(projection);\n assert.deepEqual(result.content('root'),control.content('root'));\n assert.deepEqual(result.summary('root'),control.summary('root'));\n assert.deepEqual(result.cycles,control.cycles);\n for(const s of statements)assert.equal(result.root(s.event),control.root(s.event));\n assert.equal(copies,count*(count-1)/2);\n rows.push({children:count,actual_child_elements_yielded_during_repeated_spread:copies,returned_descendants:result.content('root').events.length,outputs_equal_to_unmodified_source:true});\n}\nprocess.stdout.write(JSON.stringify({claim:'Actual source-instrumented child-list spread element counts; no wall-clock speed claim.',rows},null,2)+'\\n');\n")
r=subprocess.run(['node',str(out/'probe.mjs'),source.as_uri()],capture_output=True,text=True)
assert source.read_bytes()==original, 'Source changed during the probe.'
print(r.stdout,end='');print(r.stderr,end='',file=sys.stderr)
raise SystemExit(r.returncode)
