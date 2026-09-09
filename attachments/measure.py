import subprocess,json,datetime,sys
from pathlib import Path
ROOMS={'gitseq':('/Users/hughpyle/play/gitseq','5d2622748872b7e2dec3fe5c59e4be73a35e0bc8'),'tailapp':('/Users/hughpyle/play/tailapp','da732b0bdaad4426ed4ad666b892d8a7c68f625f'),'chess':('/Users/hughpyle/play/gitseq-chess','1b96687964cbba5a6089c526d7136b32ba9488a5'),'inventory':('/Users/hughpyle/play/gitseq-inventory','5abaa0f151cd76dcf02a7bd669358ce94b67f4a6')}
def git(repo,*args,input=None):
 p=subprocess.run(['git','-C',repo,*args],input=input,capture_output=True)
 if p.returncode:raise RuntimeError((args,p.returncode,p.stderr.decode(errors='replace')))
 return p.stdout

def batch(repo,specs):
 raw=git(repo,'cat-file','--batch',input=('\n'.join(specs)+'\n').encode());pos=0;rows=[]
 for spec in specs:
  end=raw.index(b'\n',pos);parts=raw[pos:end].split();pos=end+1
  if parts[-1:]==[b'missing']:
   rows.append(None);continue
  if len(parts)!=3 or parts[1] not in [b'commit',b'tree',b'blob']:raise ValueError((spec,parts))
  size=int(parts[2]);body=raw[pos:pos+size]
  if len(body)!=size or raw[pos+size:pos+size+1]!=b'\n':raise ValueError('incomplete object')
  pos+=size+1;rows.append((parts[0].decode(),parts[1].decode(),body))
 if pos!=len(raw):raise ValueError('extra batch bytes')
 return rows

def tree_entries(body,oidbytes):
 pos=0;entries=[]
 while pos<len(body):
  sp=body.index(b' ',pos);nul=body.index(b'\0',sp);mode=body[pos:sp];name=body[sp+1:nul].decode('utf8');oid=body[nul+1:nul+1+oidbytes]
  if len(oid)!=oidbytes or mode not in [b'100644',b'100755']:raise ValueError((mode,name))
  entries.append((name,oid.hex()));pos=nul+1+oidbytes
 return entries

pinned=json.load(open(sys.argv[1]))['rooms'] if len(sys.argv)>1 else {}
result={'measured_utc':datetime.datetime.now(datetime.timezone.utc).isoformat(),'scope':'Sizes of local accepted event records at pinned sequence heads; genesis excluded. No refused-attempt or demand count. Bytes measured, not a fresh signature audit or timing benchmark.','rooms':{}}
for name,(repo,genesis) in ROOMS.items():
 fmt=git(repo,'rev-parse','--show-object-format').decode().strip();oidbytes={'sha1':20,'sha256':32}[fmt]
 ref='refs/seq/'+genesis;head=pinned[name]['head'] if name in pinned else git(repo,'rev-parse',ref).decode().strip();history=git(repo,'rev-list','--first-parent',head).decode().splitlines()
 if history[-1]!=genesis:raise ValueError('unexpected genesis')
 commits=history[:-1];n=len(commits)
 objects=batch(repo,commits);payloads=batch(repo,[x+':event' for x in commits]);attrees=batch(repo,[x+':attachments' for x in commits]);entries=[]
 for t in attrees:
  if t is None:entries.append([])
  else:
   if t[1]!='tree':raise ValueError('attachment non-tree')
   entries.append(tree_entries(t[2],oidbytes))
 oids=sorted({oid for e in entries for _,oid in e});blobs=batch(repo,oids) if oids else [];sizes={}
 for expected,obj in zip(oids,blobs):
  if obj is None or obj[0]!=expected or obj[1]!='blob':raise ValueError('missing/non-blob attachment')
  sizes[expected]=len(obj[2])
 events=[]
 for idx,(oid,commit,payload,atts) in enumerate(zip(commits,objects,payloads,entries)):
  if commit is None or commit[0]!=oid or commit[1]!='commit' or payload is None or payload[1]!='blob':raise ValueError('missing record')
  envelope=commit[2].split(b'\n\n',1)[1];a=[{'name':nm,'bytes':sizes[ob]} for nm,ob in atts]
  events.append({'commit':oid,'sequence':n-idx,'envelope':len(envelope),'payload':len(payload[2]),'attachments':a,'total':len(envelope)+len(payload[2])+sum(x['bytes'] for x in a)})
 result['rooms'][name]={'repo':repo,'ref':ref,'head':head,'sequence_depth':n,'commit_count_including_genesis':len(history),'events_measured':len(events),'attachment_events':sum(bool(x['attachments']) for x in events),'attachment_count':sum(len(x['attachments']) for x in events),'max_event':max(events,key=lambda x:x['total']),'max_without_attachments':max((x for x in events if not x['attachments']),key=lambda x:x['total'],default=None),'events':events}
 print(json.dumps({k:v for k,v in result['rooms'][name].items() if k not in ['events','repo','ref']}),flush=True)
Path(sys.argv[2] if len(sys.argv)>2 else '/tmp/planner-accepted-size-census-20260909.json').write_text(json.dumps(result,indent=2)+'\n')
