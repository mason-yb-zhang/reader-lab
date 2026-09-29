"""Test and build the MIPS reader-lab release payload. Does not publish or install."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import struct
import subprocess

root=Path(__file__).resolve().parent
p=argparse.ArgumentParser(description=__doc__)
p.add_argument('--go',default=shutil.which('go'))
p.add_argument('--version',default='0.1.0')
a=p.parse_args()
if not a.go:p.error('Go 1.26+ required; provide --go PATH')
if not all(part.isdigit() for part in a.version.split('.')):p.error('numeric dotted version required')
env=os.environ.copy();env.update(GOTOOLCHAIN='local',GOWORK='off',CGO_ENABLED='0')
for field in ['GOOS','GOARCH','GOFLAGS']:env.pop(field,None)
for module in ['c1device','book-reader']:
    cwd=root/'App'/module
    subprocess.run([a.go,'test','-count=1','./...'],cwd=cwd,env=env,check=True)
    subprocess.run([a.go,'vet','./...'],cwd=cwd,env=env,check=True)
payload=root/'build'/('v'+a.version)/'payload'
if payload.exists():p.error('Payload directory exists; use a fresh version/output checkout')
(payload/'bin').mkdir(parents=True)
env.update(GOOS='linux',GOARCH='mipsle',GOMIPS='hardfloat')
cwd=root/'App/book-reader'
subprocess.run([a.go,'vet','./...'],cwd=cwd,env=env,check=True)
binary=payload/'bin/reader-lab'
subprocess.run([a.go,'build','-buildvcs=false','-trimpath','-ldflags',f'-s -w -X main.version={a.version}','-o',str(binary),'.'],cwd=cwd,env=env,check=True)
data=binary.read_bytes()
assert data[:7]==b'\x7fELF\x01\x01\x01'
assert struct.unpack_from('<H',data,18)[0]==8
flags=struct.unpack_from('<I',data,36)[0]
assert flags&0xf0000000 in (0x50000000,0x70000000) and flags&0xf000==0x1000
phoff=struct.unpack_from('<I',data,28)[0];phsize,phnum=struct.unpack_from('<HH',data,42)
assert all(struct.unpack_from('<I',data,phoff+i*phsize)[0] not in (2,3) for i in range(phnum))
shoff=struct.unpack_from('<I',data,32)[0];shsize,shnum=struct.unpack_from('<HH',data,46);abi=None
for i in range(shnum):
    pos=shoff+i*shsize
    if struct.unpack_from('<I',data,pos+4)[0]==0x7000002a:
        off=struct.unpack_from('<I',data,pos+16)[0];abi=struct.unpack_from('<HBBBBBB',data,off)
assert abi and abi[1]==32 and abi[2] in (1,2) and abi[3:]==(1,1,0,1),abi
shutil.copytree(root/'assets/fonts',payload/'assets/fonts')
shutil.copytree(cwd/'assets/fusion12-licenses',payload/'licenses/fusion12')
shutil.copy2(cwd/'assets/font-LICENSE.txt',payload/'licenses/Unifont-LICENSE.txt')
shutil.copy2(cwd/'THIRD_PARTY_NOTICES.md',payload/'licenses/THIRD_PARTY_NOTICES.md')
shutil.copy2(root/'LICENSE',payload/'LICENSE')
shutil.copy2(root/'UPSTREAM-LICENSE-NOTICE.txt',payload/'UPSTREAM-LICENSE-NOTICE.txt')
(payload/'SOURCE.txt').write_text('Corresponding source and build instructions (GPL-3.0):\nhttps://github.com/mason-yb-zhang/reader-lab/tree/v'+a.version+'\nFonts retain their bundled OFL licenses.\n',encoding='utf-8',newline='\n')
paths=[f for f in sorted(payload.rglob('*')) if f.is_file()]
assert all(f.stat().st_size<=16<<20 for f in paths)
assert sum(f.stat().st_size for f in paths)<=64<<20
manifest='\n'.join(hashlib.sha256(f.read_bytes()).hexdigest()+'  '+f.relative_to(payload).as_posix() for f in paths)+'\n'
(payload/'SHA256SUMS').write_text(manifest,encoding='utf-8',newline='\n')
report={'version':a.version,'ELF':'ELF32 MIPS little-endian o32 static hardfloat','flags':hex(flags),'abi':abi,'binary_sha256':hashlib.sha256(data).hexdigest(),'binary_bytes':len(data),'payload_bytes':sum(f.stat().st_size for f in payload.rglob('*') if f.is_file())}
(payload.parent/'build-report.json').write_text(json.dumps(report,indent=2),encoding='utf-8')
print(json.dumps(report,indent=2))
