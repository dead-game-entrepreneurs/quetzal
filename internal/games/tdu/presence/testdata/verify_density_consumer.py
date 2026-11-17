"""Verify1454 reply scalar widths and conditionalbuffer parsing in retailcode.
Usage: python3 verify_density_consumer.py /path/to/tdu-server/tools
Outputs native-density-consumer.json; requires user's ELF through siblingharness.
No original density-selection/counting algorithm is claimed by these fixtures.
"""
from pathlib import Path
import struct,sys,json,runpy,contextlib,io
with contextlib.redirect_stdout(io.StringIO()):
 harness=runpy.run_path(str(Path(__file__).with_name('verify_senders.py')))
Native=harness['Native'];R=harness['R']
n=Native();stream,buf=0x1c00000,0x1c01000;results=[]
for total in [3,0]:
 n.write(stream,struct.pack('<9I',0x55ef04,0,0,0,4096,0,0,buf,1));n.run(0x13a420,[stream,0x1454]);n.run(0x13a2b0,[stream,449]);n.run(0x13a2b0,[stream,total]);grid=bytearray(449);grid[448]=total
 if total:
  n.write(0x1c03000,grid);n.run(0x13a000,[stream,0x1c03000,len(grid)])
 size=struct.unpack('<I',n.read(stream+0x14,4))[0];action=n.read(buf,size)
 n.write(stream+0x14,struct.pack('<I',2));n.write(0x180e139,b'\xaa'*449)
 n.put(17,stream);n.put(20,0x1800000);n.hooks[0x41fb58]=lambda:n.u.emu_stop()
 try:n.run(0x41f678,[])
 except AssertionError:assert n.u.reg_read(R.UC_MIPS_REG_PC)==0x41fb58
 assert struct.unpack('<II',n.read(0x180e6b8,8))==(total,449)
 pos=struct.unpack('<I',n.read(stream+0x14,4))[0];assert pos==len(action)
 if total:assert n.read(0x180e139,449)==grid
 else:assert n.read(0x180e139,449)==b'\xaa'*449
 results.append({'secondScalar':total,'cellCount':449,'actionHex':action.hex(),'consumed':pos,'copiedBuffer':bool(total)})
print(json.dumps({'provenance':'Retail1454writers and nativeconsumer41f678 execute; actualcountfields, conditionalbuffer copy and exactcursor validated','entries':results},indent=2))
