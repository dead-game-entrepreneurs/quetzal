"""Execute native SessionMaster serializers against synthetic runtime records.

Usage: python3 verify_native.py /path/to/tdu-server/tools
Requires sibling presence/testdata/verify_senders.py's documented MIPS64 harness.
Original ELF stays private; no proprietary bytes are emitted into the fixture.
Native field writers, hashing, descriptors and D572 envelope execute unchanged;
only the harness's documented outer construction and input accessors are mocked.
"""
from pathlib import Path
import sys,struct,json
harness=Path(__file__).resolve().parents[2]/'presence/testdata/verify_senders.py'
exec(harness.read_text().split('\nn=Native(')[0])
n=Native()
def bounded_outer(a):
 n.write(a,bytes(0x690));n.write(a,struct.pack('<I',0x1901000));n.write(a+0x24,struct.pack('<9I',0x55ef04,0,0,0,4096,0,0,0x1903000,1))
n.init_outer=bounded_outer;n.hooks[0x12bfa0]=n.outer_ctor
def normalize(begin,end):
 for a in range(begin,end,4):
  w=struct.unpack('<I',n.read(a,4))[0];op=w>>26;new=w
  if op==30:new=(w&0x03ffffff)|(55<<26)
  elif op==31:new=(w&0x03ffffff)|(63<<26)
  elif op==28 and w&63==9 and (w>>6)&31==14:new=(((w>>16)&31)<<21)|(((w>>11)&31)<<11)|45
  if new!=w:n.write(a,struct.pack('<I',new));n.substitutions[hex(a)]=hex(w)
for a,b in [(0x137000,0x138000),(0x12f820,0x130030),(0x135000,0x135c00),(0x12db60,0x12db90)]:normalize(a,b)
def u32(a,x):n.write(a,struct.pack('<I',x))
def stream(a,buf=0x1905000):n.write(a,struct.pack('<9I',0x55ef04,0,0,0,4096,0,0,buf,1))
def value(a):return n.read(struct.unpack('<I',n.read(a+0x1c,4))[0],struct.unpack('<I',n.read(a+0x14,4))[0]).hex()
# FreeRide native runtime object, matching synthetic schema.json.
d=0x180f000;n.write(d,bytes(0x340));u32(d,0x55f690);stream(d+0x134)
n.write(d+8,struct.pack('<Q',0x0102030405060708));u32(d+0x80,0x11121314)
u32(d+0x274,0xc0000201);n.write(d+0x10,struct.pack('<4I',8889,0xc0000202,8889,22));n.write(d+0x264,b'\3')
n.write(d+0x28,b'TestDriveV2.30\0');n.write(d+0x48,b'FreeRideV5N37-8max\0');u32(d+0x68,2);u32(d+0x278,8);n.write(d+0x280,b'\1')
n.write(d+0x284,struct.pack('<4f',-1500.5,100.25,1500.5,3100.25));n.write(d+0x294,bytes(range(128)))
n.run(0x137420,[d]);free=value(d+0x134)
# SessionClientV2 native record serializes into its own inherited stream.
c=0x1810000;n.write(c,bytes(0x200));stream(c,0x1906000);u32(c,0x55f6d0);n.write(c+0x11c,b'Driver\0');u32(c+0x150,1)
u32(c+0x12c,0xc0000201);n.write(c+0x130,struct.pack('<H',8122)+b'\3');u32(c+0x134,0x12345678)
n.write(c+0x138,struct.pack('<4I',1,2,3,4));u32(c+0x118,0xc0000203);n.write(c+0xb8,struct.pack('<H',8889));n.write(c+0xf0,struct.pack('<H',2));u32(c+0xec,44);n.write(c+0xc0,bytes(range(44)));n.write(0x576808,struct.pack('<H',0xfae0))
n.run(0x137da0,[c]);client=value(c)
# Restriction sender: actual request + D572 wrapper, with all scalar parameters.
n.write(0x576770,struct.pack('<H',0x31e0))
n.run(0x12fba0,[0,0,0x11121314,0,0,0,0,8],stack=struct.pack('<QQQQ',0x1234,0xfffe,0x5678,9))
restriction=n.frames[-1]
n.write(0x576728,struct.pack('<H',0xa518));n.run(0x12fa70,[0,0,d]);update=n.frames[-1]
bl=0x1812000;n.write(bl,bytes(0x200));stream(bl,0x1907000)
n.write(0x576798,struct.pack('<H',0xdc69));n.write(0x576730,struct.pack('<H',0xe2dd))
def now():n.u.reg_write(R.UC_MIPS_REG_F0,0);n.ret()
n.hooks[0x10a400]=now
n.run(0x12f820,[0x1811000,0,c,bl,d,0x12345678,0]);create=n.frames[-1]
# BaseSession's full-width legacy scalars and optional opaque object.
n.write(d+0x20,struct.pack('<Q',0x1020304050607080));n.write(d+0x6c,struct.pack('<5I',0x11223344,0x22334455,0xfffffffe,0x33445566,0x44556677))
n.run(0x137950,[d]);base=value(d+0x134)
endpoint=0x1813000;n.write(endpoint,bytes(0x6c));n.write(endpoint+0x68,struct.pack('<I',0xc0000201));n.write(endpoint+8,struct.pack('<H',45678));n.write(endpoint+0x40,struct.pack('<H',2));n.write(0x5767c0,struct.pack('<H',0x269e));stream(0x1814000,0x1908000)
n.run(0x1359d0,[endpoint,0x1814000]);source=value(0x1814000)
fixture={'provenance'  :'US SLUS_214.90 native137420,137da0 serializers and12fba0 restriction sender executed in limited EE-to-MIPS64 harness; synthetic runtime inputs only','elfSHA256':'620ea714b1dfd2d003a9791e5fabc2e8c9f1f6ac5c908f5873cd7e54972c7bf5','free_hex':free,'client_hex':client,'restriction_frame_hex':restriction,'update_frame_hex':update,'create_frame_hex':create,'base_hex':base,'source_endpoint_hex':source}
print(json.dumps(fixture,indent=2))
