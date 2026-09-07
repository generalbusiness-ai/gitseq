from pathlib import Path
import subprocess,json,os
w=Path('/tmp/git144-probe-7d32e0');files={n:w/n for n in ['ui/src/lib/store.ts','internal/app/app.go']};source={n:p.read_text() for n,p in files.items()};env=os.environ.copy();env['GOTOOLCHAIN']='go1.26.7';env['GOWORK']='off';out=[]
mutations=[
 ('missing-profile-proof','ui/src/lib/store.ts','Boolean(next.profile) && next.profile === retainedProfile','next.profile === retainedProfile',['node','--test','ui/test/codex-profile-missing-probe.test.mjs']),
 ('profile-equality','ui/src/lib/store.ts','Boolean(next.profile) && next.profile === retainedProfile','Boolean(next.profile)',['node','--test','ui/test/codex-profile-missing-probe.test.mjs']),
 ('identity-application','internal/app/app.go','sha256.Sum256([]byte(selected.projectionProfile()))','sha256.Sum256([]byte(selected.foldVersion))',['go','test','./internal/app','-run','^TestProfileFollowsTheProjectionProfileIdentity$','-count=1','-timeout=30s']),
 ('identity-fold','internal/app/app.go','sha256.Sum256([]byte(selected.projectionProfile()))','sha256.Sum256([]byte(selected.application))',['go','test','./internal/app','-run','^TestProfileFollowsTheProjectionProfileIdentity$','-count=1','-timeout=30s'])]
try:
 for name,file,old,new,args in mutations:
  assert source[file].count(old)==1,(name,source[file].count(old))
  for n,p in files.items():p.write_text(source[n])
  files[file].write_text(source[file].replace(old,new,1))
  r=subprocess.run(args,cwd=w,env=env,text=True,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,timeout=60)
  log=Path('/tmp/git144-7c999-mutant-'+name+'.log');log.write_text(r.stdout)
  caught=r.returncode!=0 and ('--- FAIL: Test' in r.stdout or 'AssertionError' in r.stdout) and '[build failed]' not in r.stdout and 'SyntaxError' not in r.stdout
  out.append({'mutation':name,'caught':caught,'log':str(log)});print(name,caught,flush=True)
finally:
 for n,p in files.items():p.write_text(source[n])
 Path('/tmp/git144-7c999-mutants.json').write_text(json.dumps(out,indent=2))
assert len(out)==4 and all(x['caught'] for x in out)
