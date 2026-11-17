"""Execute TDU friends20/10 request builder and response-row reader on synthetic data.
Usage: python3 verify_friends.py /path/to/tdu-server/tools > native-friends.json
Uses the user's hash-checked ELF and the neighboring limited MIPS64 harness.
SDK scalar/bool/String readers and writers execute real instructions; stream/job
lifecycle, scheduling, String allocation and system memory routines are mocked.
The request fixture starts at the method selector, not the outer RMC envelope.
"""
from pathlib import Path
import sys,struct,json
harness=Path(__file__).resolve().parents[2]/'presence/testdata/verify_senders.py'
exec(harness.read_text().split('\nn=Native(')[0])
def put(n,a,v):n.write(a,struct.pack('<I',v))
def word(n,a):return struct.unpack('<I',n.read(a,4))[0]
def fresh(data=b''):
 n=Native()
 for begin,end in [(0x1f8f40,0x1fa090),(0x234c00,0x235180),(0x2361e0,0x236560),(0x2378c0,0x237930),(0x23b170,0x23b210)]:
  for a in range(begin,end,4):
   w=struct.unpack('<I',n.read(a,4))[0];op=w>>26;new=w
   if op==30:new=(w&0x03ffffff)|(55<<26)
   elif op==31:new=(w&0x03ffffff)|(63<<26)
   elif op==28 and w&63==9 and (w>>6)&31==14:new=(((w>>16)&31)<<21)|(((w>>11)&31)<<11)|45
   elif a in (0x2362f8,0x236328):new=(28<<26)|(w&0x03fff800)|2
   if new!=w:n.write(a,struct.pack('<I',new))
 obj,buf=0x1801000,0x1802000
 n.write(obj,struct.pack('<5I',0,0,buf,len(data),4096));n.write(buf,data)
 n.write(0x56f978,b'\1')
 return n,obj,buf
requests=[]
for filter,flag in [(0,0),(1,1),(2,0),(3,1),(255,1)]:
 n,obj,buf=fresh();client,fp,bp=0x1800000,0x1803000,0x1803001
 n.write(fp,bytes([filter,flag]))
 def init_stream():
  a=n.get(4);n.write(a,bytes(60));put(n,a+4,obj);n.ret(a)
 n.hooks[0x21a800]=init_stream
 for a in [0x20c910,0x1e7780,0x239ec0,0x21a8e0]:n.hooks[a]=lambda:n.ret()
 n.hooks[0x237750]=lambda:n.ret(1);n.hooks[0x238280]=lambda:n.ret(1)
 n.run(0x1f9750,[client,0,fp,bp,0])
 wire=n.read(buf,word(n,obj+0xc));assert wire==struct.pack('<I',10)+bytes([filter,flag])
 requests.append({'entrypoint':'0x1f9750','filter':filter,'flag':flag,'methodAndArgsHex':wire.hex(),'argsHex':wire[4:].hex()})
def string(s):return struct.pack('<H',len(s)+1)+s+b'\0'
row=struct.pack('<I',0x11223344)+string(b'Bee')+bytes([0x7f])+struct.pack('<I',0xaabbccdd)+string(b'state\xff')
n,obj,buf=fresh(row);stream,result,heap=0x1804000,0x1805000,[0x1810000]
n.write(stream,struct.pack('<3I',0,obj,0))
def resize_string():
 a,size=n.get(4),n.get(5);put(n,a,heap[0]);n.write(heap[0],bytes(size));heap[0]+=size+16;n.ret(a)
n.hooks[0x205680]=resize_string
n.run(0x1f8f80,[stream,result])
assert word(n,stream+8)==len(row)
def text_hex(a):
 p=word(n,a);out=b''
 while n.read(p,1)!=b'\0':out+=n.read(p,1);p+=1
 return out.hex()
parsed={'pid':word(n,result+4),'nameHex':text_hex(result+8),'relationship':n.read(result+0xc,1)[0],'details':word(n,result+0x10),'statusHex':text_hex(result+0x14)}
assert parsed=={'pid':0x11223344,'nameHex':b'Bee'.hex(),'relationship':0x7f,'details':0xaabbccdd,'statusHex':b'state\xff'.hex()}
# Execute the empty-list reader as used by response dispatcher case0x800a.
n,obj,buf=fresh(bytes(4));stream,result=0x1804000,0x1805000;n.write(stream,struct.pack('<3I',0,obj,0))
n.run(0x1e6fd0,[result]);n.hooks[0x1e6e80]=lambda:n.ret();n.run(0x1f9f60,[stream,result])
assert word(n,stream+8)==4 and word(n,result)==0
print(json.dumps({'elfSHA256':'620ea714b1dfd2d003a9791e5fabc2e8c9f1f6ac5c908f5873cd7e54972c7bf5','scope':'Real method10 request builder, scalar/bool/SDK String primitives, row reader and empty-list traversal; synthetic inputs, no tester capture','requests':requests,'row':{'entrypoint':'0x1f8f80','wireHex':row.hex(),**parsed},'emptyList':{'entrypoint':'0x1f9f60','wireHex':'00000000','consumed':4,'count':0}},indent=2))
