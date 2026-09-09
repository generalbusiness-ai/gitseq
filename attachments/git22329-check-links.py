import re,json,pathlib,subprocess,sys
repo=pathlib.Path('/Users/hughpyle/play/gitseq-worktrees/staleness-plan-recovery')
p=pathlib.Path(sys.argv[1]) if len(sys.argv)>1 else repo/'notes/2026-09-06-staleness-plan-recovery.md'
s=p.read_text();rows=[]
links=re.findall(r'https://github\.com/generalbusiness-ai/gitseq/blob/([0-9a-f]{40})/([^\s)#]+)#L([0-9]+)',s)
assert links,'no source links'
for head,path,line in links:
 blob=subprocess.check_output(['git','show',head+':'+path],cwd=repo,stderr=subprocess.PIPE);lines=blob.decode().splitlines();n=int(line)
 assert 1<=n<=len(lines),(path,n)
 assert (repo/path).read_bytes()==blob,(path,'source changed since cited baseline')
 assert '`'+path+':'+line+'`' in s,(path,'no matching Gitseq preview token')
 rows.append({'commit':head,'path':path,'line':n,'source_line':lines[n-1],'same_bytes_as_checkout':True})
tokens=re.findall(r'`((?:internal|cmd|ui)/[^`\s]+\.(?:go|ts)|(?:SKILL|AGENTS)\.md):([0-9]+)`',s)
assert {(p,n) for _,p,n in links}==set(tokens),'source hyperlinks and preview tokens differ'
print(json.dumps({'checked':len(rows),'unique':len({(r['path'],r['line']) for r in rows}),'links':rows},indent=2))
