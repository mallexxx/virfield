#!/usr/bin/env python3
"""Stage a local v2 release. Does not stop services or alter MCP configuration."""
import argparse, datetime, json, os, pathlib, shutil, sqlite3, subprocess

p=argparse.ArgumentParser();p.add_argument('--source-config',required=True);p.add_argument('--destination',required=True);a=p.parse_args()
source_config=pathlib.Path(a.source_config).resolve();config=json.loads(source_config.read_text());source=pathlib.Path(config['state_dir']);dest=pathlib.Path(a.destination).resolve();repo=pathlib.Path(__file__).resolve().parents[1]
if dest.exists():raise SystemExit('Destination already exists; refusing to replace a deployment')
os.umask(0o077);dest.mkdir(mode=0o700);(dest/'bin').mkdir(mode=0o700)
for name in ('virfield','virfieldd','virfield-mcp','virfield-lume'):
 shutil.copy2(repo/'bin'/name,dest/'bin'/name);(dest/'bin'/name).chmod(0o755)
with sqlite3.connect(f'file:{source / "state.db"}?mode=ro',uri=True) as src,sqlite3.connect(dest/'state.db') as dst:
 active=[json.loads(row[0]) for row in src.execute("select body from leases where state != 'released'")]
 if any(x['state']!='image_ready' for x in active):raise SystemExit('Release all leases before staging')
 if src.execute("select count(*) from jobs where state in ('queued','running','needs_attention')").fetchone()[0]:raise SystemExit('Resolve jobs before staging')
 src.backup(dst)
 if dst.execute('pragma integrity_check').fetchone()[0]!='ok':raise SystemExit('Database integrity check failed')
for l in active:
 folder=dest/'images'/l['id'];folder.mkdir(parents=True,mode=0o700)
 for name in ('credentials.json','verification.json','provision-verification.json'):
  f=source/'images'/l['id']/name
  if f.exists():shutil.copy2(f,folder/name);(folder/name).chmod(0o600)
shutil.copy2(config['token_file'],dest/'token');(dest/'token').chmod(0o600);(dest/'state.db').chmod(0o600)
(dest/'cache').mkdir(mode=0o700)
for f in (source/'cache').glob('*.ipsw'):subprocess.run(['/bin/cp','-c',str(f),str(dest/'cache'/f.name)],check=True)
config['state_dir']=str(dest);config['token_file']=str(dest/'token');config['resource_limits']={'cpu':12,'memory_bytes':48<<30,'disk_reserve_bytes':20<<30};config['storage_paths']={'home':str(pathlib.Path.home()/'.lume')}
# Source config must describe the verified image; never silently change its policy.
config['templates'][0]['id']='macos27'
(dest/'config.json').write_text(json.dumps(config,indent=2)+'\n');(dest/'config.json').chmod(0o600)
backup=dest/'migration-backup';backup.mkdir(mode=0o700)
for i,f in enumerate([pathlib.Path.home()/'.codex/config.toml',pathlib.Path.home()/'.claude.json',pathlib.Path.home()/'.claude/mcp.json',pathlib.Path.home()/'.claude/.mcp.json',pathlib.Path.home()/'Developer/eagle-dash/services.yaml']):
 if f.exists():shutil.copy2(f,backup/f'{i}-{f.name}');(backup/f'{i}-{f.name}').chmod(0o600)
legacy=pathlib.Path.home()/'.virfield/state.db'
if legacy.exists():
 with sqlite3.connect(f'file:{legacy}?mode=ro',uri=True) as src,sqlite3.connect(backup/'v1-state.db') as dst:src.backup(dst)
 (backup/'v1-state.db').chmod(0o600)
(backup/'manifest.json').write_text(json.dumps({'created_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'source_state':str(source),'deployment':str(dest)},indent=2)+'\n')
print('Staged binaries, verified SQLite snapshot, image identities, media cache and private migration backup:',dest)
