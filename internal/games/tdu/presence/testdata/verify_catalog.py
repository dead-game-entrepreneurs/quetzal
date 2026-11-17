"""Validate US density catalog counts using the user's private metadata.
Usage: python3 verify_catalog.py /path/to/tdu-server/tools Hawai.bnk AMHMissions.bin
No game code or assets are included. Reuses the sender verifier's MIPS64 harness.
The mission parser stops immediately after its native header-count store, before
allocation/record construction; the base loader and combined count execute fully.
"""
from pathlib import Path
import struct,sys,runpy,contextlib,io,hashlib
with contextlib.redirect_stdout(io.StringIO()):
 harness=runpy.run_path(str(Path(__file__).with_name('verify_senders.py')))
Native=harness['Native'];R=harness['R']
assert hashlib.sha256(Path(sys.argv[2]).read_bytes()).hexdigest()=='dd20967bee3e5287e1b149237996f68b4908db3318a5ba992eb537e4605d74b6'
assert hashlib.sha256(Path(sys.argv[3]).read_bytes()).hexdigest()=='66921a94191bf48cc78f2a3b639d2d8d68eb1b4a0effd0a91167c8b7607fa97a'
n=Native()
def normalize(begin,end):
 for a in range(begin,end,4):
  w=struct.unpack('<I',n.read(a,4))[0];op=w>>26;new=w
  if op==30:new=(w&0x03ffffff)|(55<<26)
  elif op==31:new=(w&0x03ffffff)|(63<<26)
  elif op==28 and w&63==9 and (w>>6)&31==14:new=(((w>>16)&31)<<21)|(((w>>11)&31)<<11)|45
  if new!=w:n.write(a,struct.pack('<I',new))
normalize(0x45f410,0x45f560);normalize(0x3033b0,0x3033d0);normalize(0x301000,0x301800);normalize(0x303510,0x303870)
b=Path(sys.argv[2]).read_bytes();p=b.index(b'.SPT');size=struct.unpack_from('<I',b,p+20)[0]+16;chunk=b[p:p+size];n.write(0x1a00000,chunk)
n.hooks[0x110460]=lambda:n.ret(0x1b00000)
n.run(0x45f410,[0x607020,0x1a00000])
basecount=struct.unpack('<I',n.read(0x607028,4))[0]
expected=b''.join(chunk[i:i+32] for i in range(32,len(chunk),32) if len(chunk[i:i+32])==32 and chunk[i+28]!=7)
assert basecount==210,(basecount,len(expected))
assert n.read(0x1b00000,basecount*32)==expected
print('native base count',basecount,'stable source order verified')
mission=Path(sys.argv[3]).read_bytes();n.write(0x1a10000,mission)
class StopAllocation(Exception):pass
def stop_alloc():raise StopAllocation()
n.hooks[0x30358c]=lambda:n.u.emu_stop()
try:n.run(0x303510,[0x64e6f0,0x1a10000,len(mission)])
except AssertionError:assert n.u.reg_read(R.UC_MIPS_REG_PC)==0x30358c
count=struct.unpack('<I',n.read(0x64e708,4))[0]
assert count==238,count
n.run(0x3033b0,[0x607020]);combined=n.get(2);assert combined==448,combined
print('native mission count',count,'combined count',combined,'type6 aggregate index',combined)
