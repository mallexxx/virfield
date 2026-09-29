#!/usr/bin/env python3
"""Replace only Virfield's client entry after installed-service acceptance."""
import json, os, pathlib, re, tempfile, tomllib
home=pathlib.Path('/Users/admin');root=home/'.virfield-v2';binary=str(root/'bin/virfield-mcp');args=['-token-file',str(root/'token')]
def atomic(path,data):
 fd,temp=tempfile.mkstemp(prefix='.virfield-migrate-',dir=path.parent)
 try:
  with os.fdopen(fd,'w') as f:f.write(data);f.flush();os.fsync(f.fileno())
  os.chmod(temp,0o600);os.replace(temp,path)
 finally:
  if os.path.exists(temp):os.unlink(temp)
p=home/'.codex/config.toml';text=p.read_text();before=tomllib.loads(text)
pattern=r'(?ms)^\[mcp_servers\.virfield\]\s*\n(.*?)(?=^\[|\Z)'
m=re.search(pattern,text)
if not m:raise SystemExit('Existing Codex Virfield entry not found; refusing guessed edit')
body=m.group(1)
for field,value in [('command',json.dumps(binary)),('args',json.dumps(args))]:
 line=field+' = '+value+'\n'
 if re.search(r'(?m)^'+field+r'\s*=',body):body=re.sub(r'(?m)^'+field+r'\s*=.*\n?',lambda _:line,body)
 else:body+=line
text=text[:m.start(1)]+body+text[m.end(1):];after=tomllib.loads(text)
a=json.loads(json.dumps(before));b=json.loads(json.dumps(after));a['mcp_servers'].pop('virfield');b['mcp_servers'].pop('virfield');assert a==b,'Unrelated Codex config changed'
atomic(p,text);print('Updated Codex Virfield command')
for p in [home/'.claude.json',home/'.claude/mcp.json',home/'.claude/.mcp.json']:
 if not p.exists():continue
 d=json.loads(p.read_text());servers=d.get('mcpServers',d)
 if 'virfield' not in servers:continue
 servers['virfield']={'type':'stdio','command':binary,'args':args};atomic(p,json.dumps(d,ensure_ascii=False,indent=2)+'\n');print('Updated',p)
