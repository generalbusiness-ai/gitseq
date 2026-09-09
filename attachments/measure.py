import subprocess, json, os, sys

REPOS = ["/Users/hughpyle/play/gitseq","/Users/hughpyle/play/tailapp",
         "/Users/hughpyle/play/gitseq-chess","/Users/hughpyle/play/gitseq-inventory"]

def git(repo, *args, input=None):
    return subprocess.run(["git","-C",repo]+list(args), capture_output=True, input=input)

def parse_tree(raw):
    # tree object: "<mode> <name>\0<20-byte oid>"
    out=[]; i=0
    while i < len(raw):
        sp = raw.index(b' ', i)
        mode = raw[i:sp]
        nul = raw.index(b'\0', sp)
        name = raw[sp+1:nul].decode('utf-8','replace')
        oid = raw[nul+1:nul+21].hex()
        out.append((mode.decode(), name, oid))
        i = nul+21
    return out

results={}
for repo in REPOS:
    refs = git(repo,"for-each-ref","--format=%(refname)","refs/seq").stdout.decode().split()
    if not refs: continue
    ref = refs[0]; genesis = ref.split('/')[-1]
    commits = git(repo,"rev-list",ref).stdout.decode().split()
    # order: rev-list is newest-first; sequence index = len-idx
    n=len(commits)
    # batch fetch attachments trees
    req = "".join(c+":attachments\n" for c in commits).encode()
    p = subprocess.run(["git","-C",repo,"cat-file","--batch"], input=req, capture_output=True)
    out = p.stdout
    events=[]   # (commit, seq, [(name,oid)])
    pos=0; idx=0
    while pos < len(out) and idx < n:
        eol = out.index(b'\n', pos)
        header = out[pos:eol].decode()
        parts = header.split()
        if len(parts)>=2 and parts[-1] in ("missing","ambiguous"):
            pos = eol+1; idx+=1; continue
        if len(parts)==3 and parts[1]=="tree":
            size=int(parts[2])
            raw = out[eol+1:eol+1+size]
            entries = parse_tree(raw)
            events.append((commits[idx], n-idx, [(nm,oid) for (_m,nm,oid) in entries]))
            pos = eol+1+size+1
            idx+=1
        else:
            # unexpected; skip line
            pos = eol+1; idx+=1
    # sizes for all blob oids
    oids = sorted({oid for _c,_s,ents in events for _nm,oid in ents})
    sizes={}
    if oids:
        p2 = subprocess.run(["git","-C",repo,"cat-file","--batch-check"],
                            input=("\n".join(oids)+"\n").encode(), capture_output=True)
        for line in p2.stdout.decode().splitlines():
            f=line.split()
            if len(f)==3 and f[1]=="blob":
                sizes[f[0]]=int(f[2])
    # commit message (signed envelope) sizes for events with attachments
    envsizes={}
    if events:
        p3 = subprocess.run(["git","-C",repo,"cat-file","--batch"],
                            input=("\n".join(c for c,_s,_e in events)+"\n").encode(), capture_output=True)
        o=p3.stdout; pos=0
        for c,_s,_e in events:
            eol=o.index(b'\n',pos); hdr=o[pos:eol].decode().split()
            sz=int(hdr[2]); body=o[eol+1:eol+1+sz]
            # commit message follows the blank line after headers
            bl = body.index(b'\n\n')
            envsizes[c]=len(body[bl+2:])
            pos=eol+1+sz+1
    # event payload blob sizes
    paysizes={}
    if events:
        p4 = subprocess.run(["git","-C",repo,"cat-file","--batch-check"],
                            input="".join(c+":event\n" for c,_s,_e in events).encode(), capture_output=True)
        for c,line in zip([c for c,_s,_e in events], p4.stdout.decode().splitlines()):
            f=line.split()
            if len(f)==3 and f[1]=="blob": paysizes[c]=int(f[2])
    results[repo]={"ref":ref,"genesis":genesis,"depth":n,
        "events":[{"commit":c,"seq":s,"env":envsizes.get(c,0),"payload":paysizes.get(c,0),
                   "att":[{"name":nm,"size":sizes.get(oid,0)} for nm,oid in ents]} for c,s,ents in events]}
    print(repo, "depth", n, "with-attachments", len(events), file=sys.stderr)

json.dump(results, open("/private/tmp/claude-501/-Users-hughpyle-play-gitseq/edad3ec5-3d64-4212-89b8-59e5bafff100/scratchpad/22093/work/measure.json","w"))
print("done")
