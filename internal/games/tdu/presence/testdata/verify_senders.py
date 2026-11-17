"""Execute retail presence senders and capture their actual edNet datagrams.

Usage: python3 verify_senders.py /path/to/tdu-server/tools > native-senders.json
Requires the user's verified ELF through ee_validation.CORE and Unicorn.
Only 128-bit stack saves/loads and low64 PCPYLD operations are normalized.
Sender ordering, signature hashing, every scalar writer (including real64-bit
mask writes) and D572 framing execute retail instructions. Memory/string
intrinsics, outer object construction/reset, service/friend/NAT input lookups,
cleanup and transport availability/append are supplied by the harness. The
native append boundary136880 captures the completed packet; no writer is mocked.
This is an independent wire fixture, not a complete EE emulator or live session.
"""
import sys,struct,json
sys.path.insert(0,sys.argv[1])
from ee_validation import EE,CORE
from unicorn import *
from unicorn import mips_const as R
class Native(EE):
 def __init__(self):
  self.u=Uc(UC_ARCH_MIPS,UC_MODE_MIPS64|UC_MODE_LITTLE_ENDIAN)
  self.u.mem_map(0x100000,0x1f00000);self.u.mem_write(0x100000,CORE[0x100:0x46fa00])
  self.regs=[getattr(R,'UC_MIPS_REG_'+str(n)) for n in range(32)]
  self.hooks={0x10b230:self.memcpy,0x27cf90:self.memcpy,0x278dc8:self.memset,0x27ca18:self.strlen,0x12aba0:self.outer_ctor,0x1902000:self.reset,0x1902004:lambda:self.ret(),0x136880:self.capture,0x1368f0:lambda:self.ret(1),0x136150:lambda:self.ret(),0x13a940:lambda:self.ret(),0x12be60:lambda:self.ret(),0x3b46e0:lambda:self.ret(0x1808000),0x3a0d90:lambda:self.ret(3)}
  self.last=[];self.substitutions={};self.actions=[];self.frames=[]
  self.u.hook_add(UC_HOOK_CODE,self.step)
  for begin,end in [(0x138000,0x13ac00),(0x12b380,0x12b520),(0x3bd670,0x3bdab8),(0x460630,0x4608d0),(0x1362b0,0x1363b0),(0x3ce550,0x3ce5c0),(0x4193c0,0x4193f0)]:
   for a in range(begin,end,4):
    w=struct.unpack('<I',self.read(a,4))[0];op=w>>26;new=w
    if op==30:new=(w&0x03ffffff)|(55<<26)
    elif op==31:new=(w&0x03ffffff)|(63<<26)
    elif op==28 and w&63==9 and (w>>6)&31==14:new=(((w>>16)&31)<<21)|(((w>>11)&31)<<11)|45
    if new!=w:self.write(a,struct.pack('<I',new));self.substitutions[hex(a)]=hex(w)
  self.write(0x4e5fb0,b'\1');self.write(0x656350,bytes(4));self.write(0x654350,bytes(4))
  self.write(0x65e760,struct.pack('<I',0x1800000));self.write(0x1808014,struct.pack('<I',1));self.write(0x1812e58,struct.pack('<I',5));self.write(0x65c5dc,struct.pack('<I',0x12345678))
  self.write(0x5eb77c,struct.pack('<I',1));self.write(0x5eb6a4,struct.pack('<I',1));self.write(0xaa6140,struct.pack('<H',0x42bb));self.write(0xaa6158,struct.pack('<H',0x55ae));self.write(0x576748,struct.pack('<H',0x510b));self.write(0x5760c0,struct.pack('<H',0xd572))
  self.write(0x1901000,struct.pack('<8I',0,0,0,0x1902000,0,0,0x1902004,0))
  self.init_outer(0x1812748)
 def memset(self):self.write(self.get(4),bytes([self.get(5)&255])*self.get(6));self.ret(self.get(4))
 def strlen(self):
  a=self.get(4);n=0
  while self.read(a+n,1)!=b'\0':n+=1
  self.ret(n)
 def init_outer(self,a):
  self.write(a,bytes(0x6a4));self.write(a,struct.pack('<I',0x1901000));self.write(a+0x24,struct.pack('<9I',0x55ef04,0,0,0,4096,0,0,0x1903000,1))
 def outer_ctor(self):a=self.get(4);self.init_outer(a);self.ret(a)
 def reset(self):
  a=self.get(4)+0x24;self.write(a+0xc,struct.pack('<3I',0,4096,0));self.write(a+0x18,bytes(4));self.ret()
 def capture(self):
  a=self.get(6);n=struct.unpack('<I',self.read(a+0x14,4))[0];p=struct.unpack('<I',self.read(a+0x1c,4))[0];frame=self.read(p,n);self.frames.append(frame.hex());assert frame[:2]==bytes.fromhex("d572");assert int.from_bytes(frame[2:4],"big")==len(frame)-4;self.actions.append(frame[4:].hex());self.ret()
 def run(self,entry,args,floats=(),stack=bytes(32)):
  self.u.reg_write(R.UC_MIPS_REG_SP,0x1fff000);self.write(0x1fff000,stack);self.put(31,0x100000);self.put(28,0x577170)
  for i,v in enumerate(args):self.put(4+i,v)
  for i,f in enumerate(floats):self.u.reg_write(getattr(R,'UC_MIPS_REG_F'+str(12+i)),struct.unpack('<I',struct.pack('<f',f))[0])
  try:self.u.emu_start(entry,0x100000,count=2000000)
  except Exception as e:raise RuntimeError(f'{e}, pc {hex(self.u.reg_read(R.UC_MIPS_REG_PC))}, last {[hex(x) for x in self.last]}')
  assert self.u.reg_read(R.UC_MIPS_REG_PC)==0x100000
n=Native();n.write(0x1809000,struct.pack('<fff',1,3,2));n.run(0x3bd670,[0,0x1809000])
n.run(0x3bd8d0,[])
n.run(0x3bd9d0,[])
n.write(0x180a000,b'A\0');n.write(0x180b040,struct.pack('<H',2));n.write(0x180c854,struct.pack('<I',1))
n.run(0x460630,[0x180c000,0x180d000,1,2,0x180a000,0x180b000,0x11223344,0x55667788],[1,3,2],struct.pack('<IIiIQII',5,0,-6,0,0x0102030405060708,7,0))
# A second track exercises the uncounted online-friend suffix in the sender.
n.write(0x654350,struct.pack('<I',0x180e000));n.write(0x180e008,struct.pack('<I',0x180e100));n.write(0x180e104,struct.pack('<I',1));n.write(0x180e218,struct.pack('<I',9));n.write(0x180e238,struct.pack('<I',10))
n.hooks[0x1e2520]=lambda:n.ret(1)
n.hooks[0x1e4da0]=lambda:n.ret(2)
n.hooks[0x1e4d80]=lambda:n.ret(0x180e200+n.get(5)*32)
n.hooks[0x1e4af0]=lambda:n.ret(1)
n.run(0x3bd670,[0,0x1809000])
fixture={'provenance':'Retail US SLUS_214.90 actual sender functions and D572 wrapper executed in limited EE-to-MIPS64 harness; transport append captured at136880','elfSHA256':'620ea714b1dfd2d003a9791e5fabc2e8c9f1f6ac5c908f5873cd7e54972c7bf5','entries':[{'name':name,'entrypoint':entry,'frameHex':frame} for name,entry,frame in zip(['track','map','session-density','update','track-friends'],['0x3bd670','0x3bd8d0','0x3bd9d0','0x460630','0x3bd670'],n.frames)]}
print(json.dumps(fixture,indent=2))
