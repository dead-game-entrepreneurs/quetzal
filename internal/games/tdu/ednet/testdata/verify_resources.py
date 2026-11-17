"""Execute native resource senders/consumer on synthetic records.
Usage: python3 verify_resources.py /path/to/tdu-server/tools > native-resources.json
Requires Unicorn and the user's hash-verified SLUS_214.90 via ee_validation.
Shared MIPS64 harness executes real stream primitives. Mocked boundaries are
memory/string intrinsics, lifecycle/reset, clock, resource-job registration,
signature diagnostics and reliable-submit capture. No field writer/reader is
mocked. Reply bytes are synthetic server-policy inputs consumed by15b5d0;
request bodies come from actual15bfd0/15be00/15bb20 instruction execution.
No complete retail client or platform I/O is emulated.
"""
from pathlib import Path
import sys,struct,json
harness=Path(__file__).resolve().parents[2]/'presence/testdata/verify_senders.py'
exec(harness.read_text().split('\nn=Native(')[0])

def fresh():
 n=Native()
 for start,end in [(0x15b000,0x15c1e0),(0x138000,0x13ad00)]:
  for a in range(start,end,4):
   w=struct.unpack('<I',n.read(a,4))[0];op=w>>26;new=w
   if op==30:new=(w&0x03ffffff)|(55<<26)
   elif op==31:new=(w&0x03ffffff)|(63<<26)
   elif op==28 and w&63==9 and (w>>6)&31==14:new=(((w>>16)&31)<<21)|(((w>>11)&31)<<11)|45
   if new!=w:n.write(a,struct.pack('<I',new))
 n.hooks[0x492d80]=lambda:n.ret(5000)
 n.hooks[0x156590]=lambda:n.ret()
 n.hooks[0x138240]=lambda:n.ret()
 n.hooks[0x1902100]=lambda:n.ret()
 n.write(0x1901100,bytes(0x50));n.write(0x190110c,struct.pack('<I',0x1902100))
 for a,v in [(0x59bfb8,0x3be2),(0x59bfc0,0xb715),(0x59bfc8,0xd976),(0x59bfd0,0x49a3)]:n.write(a,struct.pack('<H',v))
 return n

def put(n,a,x):n.write(a,struct.pack('<I',x))
def word(n,a):return struct.unpack('<I',n.read(a,4))[0]
def stream(n,a,buf=0x1905000,data=None):
 n.write(a,struct.pack('<9I',0x55ef04,0,0,0 if data is None else len(data),4096,0,0,buf,1))
 if data is not None:n.write(buf,data)
def value(n,a):return n.read(word(n,a+0x1c),word(n,a+0x14)).hex()
def obj(n):
 a=0x1800000;n.write(a,bytes(0xa00));put(n,a,0x1901100);put(n,a+0x32c,0x1901100);stream(n,a+0x350)
 put(n,a+0x2b8,1);put(n,a+0x9c0,10);put(n,a+0x9c4,1000)
 return a

def request(kind,facility=1,user=0,name='netconfig.xml',file=1,offset=0):
 n=fresh();a=obj(n);frames=[]
 def capture():frames.append(value(n,n.get(4)+0x24));n.ret(0)
 n.hooks[0x12b170]=capture
 put(n,a+8,facility);put(n,a+0x218,user);n.write(a+0x10,name.encode()+b'\0')
 if kind=='get':
  put(n,a+0x29c,1);put(n,a+0x2a0,0);put(n,a+700,file);put(n,a+0x2ac,offset);entry=0x15bb20
 elif kind=='upload':put(n,a+0x290,0x55667788);entry=0x15be00
 else:entry=0x15bfd0
 n.run(entry,[a]);assert len(frames)==1
 return {'name':kind,'entrypoint':hex(entry),'actionHex':frames[0]}

def consume(name,wire,offset=0,total=11):
 n=fresh();a=obj(n);put(n,a+0x210,1);put(n,a+0x2ac,offset);put(n,a+0x2a8,total);put(n,a+0x28c,0x1810000)
 pending,reader=0x1820000,0x1830000;n.write(pending,bytes(0x600));stream(n,reader,0x1906000,wire)
 put(n,pending+0x5cc,reader);n.write(pending+0x2c,struct.pack('<H',0x3be2 if name.startswith('open') else 0xd976))
 n.run(0x15b5d0,[a,pending])
 return {'name':name,'entrypoint':'0x15b5d0','actionHex':wire.hex(),'consumed':word(n,reader+0x14),'state':word(n,a+0x210),'error':word(n,a+0x214),'fileID':word(n,a+700),'total':word(n,a+0x2a8),'offset':word(n,a+0x2ac),'stage':word(n,a+0x29c),'resumeAt':word(n,a+0x2a0),'retries':word(n,a+0x9c0),'timeout':word(n,a+0x9c4),'copiedHex':n.read(0x1810000+offset,max(0,word(n,a+0x2ac)-offset)).hex()}

requests=[request('open'),request('upload',0x7f,0x11223344,'fixture.bin'),request('get'),request('get',file=0x11223344,offset=0x55667788)]
chunk=bytes.fromhex('00aaff7e61')
replies=[consume('open-success',bytes.fromhex('b71500')+struct.pack('>4I',1,11,10,1000)),consume('open-error',bytes.fromhex('b71501')+bytes(16)),consume('get-success',bytes.fromhex('49a300')+struct.pack('>IIHH',1,0,0,len(chunk))+chunk),consume('get-wait',bytes.fromhex('49a302')+struct.pack('>IIHH',1,0,0x1234,0)),consume('get-error',bytes.fromhex('49a301')+struct.pack('>IIHH',1,0,0,0)),consume('get-final',bytes.fromhex('49a300')+struct.pack('>IIHH',1,6,0,len(chunk))+chunk,offset=6)]
replies.append(consume('get-complete',bytes.fromhex('49a300')+struct.pack('>IIHH',1,0,0,11)+chunk+b'X'+chunk))
replies.append(consume('get-unknown',bytes.fromhex('49a301')+struct.pack('>IIHH',0x11223344,0x55667788,0,0)))
assert replies[6]['state']==3 and replies[6]['offset']==11
assert replies[7]['state']==3 and replies[7]['error']==1
assert replies[0]['fileID']==1 and replies[0]['total']==11 and replies[0]['retries']==10 and replies[0]['timeout']==1000
assert replies[1]['state']==3 and replies[1]['error']==1
assert replies[2]['copiedHex']==chunk.hex() and replies[2]['offset']==5 and replies[2]['stage']==1
assert replies[3]['resumeAt']==5000+0x1234 and replies[3]['stage']==3
assert replies[4]['state']==3 and replies[4]['error']==1
assert replies[5]['offset']==11 and replies[5]['state']==3
print(json.dumps({'provenance':'Actual US retail Open/upload/Get senders and resource reply consumer with real edNet stream primitives in limited EE-to-MIPS64 harness; synthetic inputs only','elfSHA256':'620ea714b1dfd2d003a9791e5fabc2e8c9f1f6ac5c908f5873cd7e54972c7bf5','requests':requests,'replies':replies},indent=2))
