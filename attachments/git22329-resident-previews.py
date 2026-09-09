import json,urllib.request,concurrent.futures,pathlib,subprocess
root=pathlib.Path('/Users/hughpyle/play/gitseq-worktrees/staleness-plan-recovery')
event='git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:327c1bec9355a828bf9f546bbdec70be6ee09e93'
head='3a4fc60cc3c31bed3915c3491c58f37cc3523aab'
clicked=json.load(open('/tmp/git22329-rendered-links.json'))['targets']
inputs=[dict(t,event=event) for t in clicked]
inputs.append({'event':event,'commit':head,'path':'notes/2026-09-06-staleness-plan-recovery.md'})
def check(inp):
 req=urllib.request.Request('http://127.0.0.1:7777/v0/preview',data=json.dumps(inp).encode(),headers={'Content-Type':'application/json','Origin':'http://127.0.0.1:7777'})
 with urllib.request.urlopen(req,timeout=30) as response:d=json.load(response)
 assert d.get('status')=='ready',(inp,d)
 assert d['commit']==head and d['path']==inp['path'],(inp,d)
 source=subprocess.check_output(['git','show',head+':'+inp['path']],cwd=root,text=True)
 n=inp.get('line');window=d.get('window',{})
 if n:
  start=window.get('start',1);end=window.get('end',len(source.splitlines()));assert start<=n<=end,(inp,window)
  assert d['content'].splitlines()[n-start]==source.splitlines()[n-1],(inp,'cited line differs')
 else:assert d['content'].rstrip('\n')==source.rstrip('\n'),(inp,'note content differs')
 return {'input':inp,'status':d['status'],'commit':d['commit'],'path':d['path'],'window':window,'source_match':True}
with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:results=list(pool.map(check,inputs))
pathlib.Path('/tmp/git22329-resident-previews.json').write_text(json.dumps({'method':'Actual targets clicked from the committed note renderer, with only the now-published owning event substituted; no path normalization. Resident admitted exact head and returned matching source lines.','results':results},indent=2))
print('PASS:',len(results),'resident windows:17 exact clicked source lines and complete committed note')
