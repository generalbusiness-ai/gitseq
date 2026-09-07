import json, subprocess, tempfile, pathlib, shutil, time, datetime, platform
CLI='/tmp/planner-gs-0775335'
ROOT=pathlib.Path(tempfile.mkdtemp(prefix='planner-artifact-batch-'))
OUT=pathlib.Path('/tmp/planner-artifact-batch-probe-20260907.json')
result={'started_utc':datetime.datetime.now(datetime.timezone.utc).isoformat(),'cli':CLI,'root':str(ROOT),'samples':[],'errors':[],'method':'Synthetic signed histories; same fixture copied per arm, 15 artifact statements each resting on seed at existing source paths. Timing excludes init, history generation, copying and verification. Per-act local CLI vs one gs batch --server -. No runtime/service/production workroom changed. Concurrent host load uncontrolled; measurements are not production latency guarantees.','environment':platform.platform()}
def run(args,cwd=None):
 r=subprocess.run(args,cwd=cwd,text=True,capture_output=True,timeout=180)
 if r.returncode: raise RuntimeError(str(args[:5])+' exit='+str(r.returncode)+' '+r.stderr[-700:]+' '+r.stdout[-300:])
 return r.stdout
def save(): OUT.write_text(json.dumps(result,indent=2)+'\n')
try:
 for depth in [100,1000]:
  template=ROOT/('template-'+str(depth));template.mkdir()
  run(['git','init','-q',str(template)])
  for i in range(15):(template/('path'+str(i)+'.txt')).write_text('synthetic artifact '+str(i)+'\n')
  run(['git','add','.'],cwd=template)
  run(['git','-c','user.name=Fixture','-c','user.email=fixture@example.invalid','commit','-q','-m','Synthetic artifact fixture'],cwd=template)
  head=run(['git','rev-parse','HEAD'],cwd=template).strip()
  init=json.loads(run([CLI,'init','--repo',str(template),'--operator','bench']))
  seed=init['seed']
  seedfile=ROOT/('seed-'+str(depth)+'.json')
  seedfile.write_text(json.dumps([{'verb':'state','kind':'assert','text':'synthetic history '+str(i),'rests_on':[seed],'idempotency_key':'seed-'+str(i)} for i in range(depth-1)]))
  run([CLI,'batch','--repo',str(template),'--as','bench','--server','-',str(seedfile)])
  acts=[{'label':'a'+str(i),'verb':'state','kind':'artifact','text':'Synthetic existing source artifact '+str(i),'body':{'path':'path'+str(i)+'.txt','commit':head},'rests_on':[seed],'idempotency_key':'artifact-'+str(i)} for i in range(15)]
  batchfile=ROOT/('artifacts-'+str(depth)+'.json');batchfile.write_text(json.dumps(acts))
  for repeat in range(3):
   for mode in (['single','batch'] if repeat%2==0 else ['batch','single']):
    repo=ROOT/(str(depth)+'-'+str(repeat)+'-'+mode);shutil.copytree(template,repo)
    began=time.perf_counter()
    if mode=='single':
     ids=[]
     for a in acts:
      ids.append(run([CLI,'state','--repo',str(repo),'--as','bench','--kind','artifact','--text',a['text'],'--body','path='+a['body']['path'],'--body','commit='+head,'--rests-on',seed,'--idempotency-key',a['idempotency_key'],'--server','-']).strip())
    else:
     report=json.loads(run([CLI,'batch','--repo',str(repo),'--as','bench','--server','-',str(batchfile)]))
     if report.get('landed')!=15 or report.get('error'):raise RuntimeError('unexpected batch report '+str(report))
     ids=[a['event'] for a in report['acts']]
    elapsed=time.perf_counter()-began
    status=json.loads(run([CLI,'status','--repo',str(repo),'--json','--server','-']))
    # CLI status is the application snapshot, not the HTTP durable/live wrapper.
    snap=status.get('durable',status)
    proj=snap['projection']; dec={x['event']:x['verdict'] for x in proj['decisions']}
    effective=all(dec.get(e)=='effective' for e in ids)
    if not effective:raise RuntimeError('ineffective fixture artifact')
    row={'depth_before':depth,'repeat':repeat+1,'mode':mode,'artifacts':len(ids),'seconds':round(elapsed,6),'depth_after':snap['depth'],'all_artifacts_effective':effective}
    result['samples'].append(row);save();print(json.dumps(row),flush=True)
    shutil.rmtree(repo)
except Exception as e:
 result['errors'].append(str(e));save();raise
finally:
 shutil.rmtree(ROOT);result['scratch_removed']=not ROOT.exists();result['finished_utc']=datetime.datetime.now(datetime.timezone.utc).isoformat();save()
