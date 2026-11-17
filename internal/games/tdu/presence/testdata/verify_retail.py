"""Regenerate the map fixture with the user's extracted US ELF, never game code.

Usage: python3 verify_retail.py /path/to/tdu-server/tools
Requires that sibling research checkout's ee_validation.py and its user-owned
work/ps2/SLUS_214.90. Normal Go tests use only the generated JSON fixture.
"""
import json
import struct
import sys
sys.path.insert(0, sys.argv[1])
from ee_validation import EE
em = EE()
em.normalize(0x138000, 0x13aa00)
stream, buf, source, dest = 0x1800000, 0x1801000, 0x1803000, 0x1805000
# Caller-owned fixed buffer, so no allocator is substituted.
em.write(stream, struct.pack('<9I', 0x55f170, 0, 0, 0, 4096, 0, 0, buf, 1))
em.write(0x4e5fb0, b'\1')
grid = bytearray(1353)
grid[0], grid[33], grid[-1] = 2, 4, 1
em.write(source, grid)
em.call(0x13a420, [stream, 0xd768])
em.call(0x13a000, [stream, source, len(grid)])
em.call(0x13a1f0, [stream, 7])
n = struct.unpack('<I', em.read(stream + 0x14, 4))[0]
action = em.read(buf, n)
assert action == struct.pack('>HH', 0xd768, 1353) + grid + struct.pack('>I', 7)
em.write(stream + 0x14, struct.pack('<I', 2))
assert em.call(0x138b80, [stream, dest, 1353]) == 1353
assert em.read(dest, 1353) == grid
assert em.call(0x1393c0, [stream]) == 7
assert struct.unpack('<I', em.read(stream + 0x14, 4))[0] == len(action)
print(json.dumps({
 'provenance': 'Retail US SLUS_214.90 scalar/buffer writers and readers via limited EE harness; no live client session',
 'elfSHA256': '620ea714b1dfd2d003a9791e5fabc2e8c9f1f6ac5c908f5873cd7e54972c7bf5',
 'writerEntrypoints': ['0x13a420', '0x13a000', '0x13a1f0'],
 'readerEntrypoints': ['0x138b80', '0x1393c0'],
 'actionHex': action.hex(), 'total': 7, 'nonzeroCells': {'0': 2, '33': 4, '1352': 1}
}, indent=2))
