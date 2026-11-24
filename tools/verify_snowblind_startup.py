#!/usr/bin/env python3
"""Verify the retail US patch state machine with synthetic HTTP/local-file inputs.

Runs the original R5900 instructions under Unicorn with low-word save/load and
pointer arithmetic translations. Hooks HTTP completion, UI, local patch lookup
and cleanup, and libc atoi, not the manifest line parser or state decisions.
No game bytes are exported.
Requires unicorn; extracted executable remains private under disc/snowblind.
"""
import argparse
import hashlib
import json
import struct
from pathlib import Path
from unicorn import Uc, UC_ARCH_MIPS, UC_HOOK_CODE, UC_MODE_LITTLE_ENDIAN, UC_MODE_MIPS32
from unicorn.mips_const import (
    UC_MIPS_REG_A0, UC_MIPS_REG_GP, UC_MIPS_REG_PC, UC_MIPS_REG_RA,
    UC_MIPS_REG_SP, UC_MIPS_REG_V0,
)

EXPECTED = '00f19fd1c63f395ec6e3dfe1736c0a9c80e63becf4497d43a5fe095169ddc8c3'
MANIFEST = b'http://sb1.patch.eidos.com/psb1a/disabled\r\n0\r\n'


def load_image(path):
    """Load the supported ELF and translate low-word EE operations for Unicorn."""
    data = bytearray(path.read_bytes())
    if hashlib.sha256(data).hexdigest() != EXPECTED:
        raise ValueError('unsupported US executable hash')
    translated = set()
    for off in range(0x80, 0x5e66b0 - 0xfff80, 4):
        word = struct.unpack_from('<I', data, off)[0]
        opcode, replacement = word >> 26, None
        if opcode in (0x1e, 0x1f, 0x37, 0x3f):
            replacement = (word & 0x3ffffff) | ((0x23 if opcode in (0x1e, 0x37) else 0x2b) << 26)
        if opcode == 0 and word & 0x3f in (0x2d, 0x2f):
            replacement = (word & ~0x3f) | (0x21 if word & 0x3f == 0x2d else 0x23)
        if opcode == 0x19:  # DADDIU -> ADDIU
            replacement = (word & 0x3ffffff) | (0x09 << 26)
        if replacement is not None:
            struct.pack_into('<I', data, off, replacement)
            translated.add(off + 0xfff80)
    cpu = Uc(UC_ARCH_MIPS, UC_MODE_MIPS32 | UC_MODE_LITTLE_ENDIAN)
    cpu.mem_map(0, 0x2000000)
    cpu.mem_write(0x100000, bytes(data[0x80:]))
    return cpu, translated


def verify(path):
    cpu, translated = load_image(path)
    obj, job, stop = 0x1000000, 0x1010000, 0x1100000
    def u32(addr): return struct.unpack('<I', cpu.mem_read(addr, 4))[0]
    def put(addr, value): cpu.mem_write(addr, struct.pack('<I', value))
    put(0x5f0138, 0x1020000)
    put(obj + 0x14, 2)
    put(obj + 0x18, 0x3f800000)
    put(obj + 0x164, job)
    cpu.mem_write(obj + 0x44, MANIFEST)
    put(job + 0x2024, len(MANIFEST))
    put(job + 0x2028, 256)
    hooks, executed, recent = [], set(), []
    # Local patch absent, no forced-download flag, UI idle, completed HTTP.
    stubs = {0x3daae0, 0x233ce0, 0x1cfe90, 0x3f40c8, 0x3e1e50,
             0x227080, 0x126528, 0x1cfdb0, 0x4fb488}
    def hook(machine, address, size, context):
        executed.add(address)
        recent.append(hex(address))
        del recent[:-12]
        if address == stop:
            machine.emu_stop()
        elif address in stubs:
            hooks.append(hex(address))
            if address == 0x3f40c8:
                machine.mem_write(machine.reg_read(UC_MIPS_REG_A0), b'\0' * 4)
            value = 0
            if address == 0x4fb488:
                raw = bytes(machine.mem_read(machine.reg_read(UC_MIPS_REG_A0), 256)).split(b'\0', 1)[0]
                value = int(raw)
            machine.reg_write(UC_MIPS_REG_V0, value)
            machine.reg_write(UC_MIPS_REG_PC, machine.reg_read(UC_MIPS_REG_RA))
    cpu.hook_add(UC_HOOK_CODE, hook)
    states = []
    for _ in range(10):
        states.append(u32(obj + 0x14))
        cpu.reg_write(UC_MIPS_REG_A0, obj)
        cpu.reg_write(UC_MIPS_REG_SP, 0x1fff000)
        cpu.reg_write(UC_MIPS_REG_GP, 0x671d70)
        cpu.reg_write(UC_MIPS_REG_RA, stop)
        try:
            cpu.emu_start(0x21e710, stop + 4, count=100000)
        except Exception as exc:
            raise RuntimeError(f"CPU stopped at {cpu.reg_read(UC_MIPS_REG_PC):08x}, last {recent}") from exc
        if cpu.reg_read(UC_MIPS_REG_PC) != stop:
            raise AssertionError('native update exceeded instruction budget')
        if cpu.reg_read(UC_MIPS_REG_V0) == 1:
            break
    assert states == [2, 3, 4, 5, 10, 0x29, 0x2a], states
    assert u32(obj + 0x28) == 0 and u32(0x5ef8b8) == 1 and u32(0x5ef8bc) == 0
    return {'kind': 'retail_patch_update_with_hooked_http_ui_and_local_storage',
            'manifest': MANIFEST.decode(), 'states': states, 'patch_error': 0,
            'download_requested': False, 'hooks': sorted(set(hooks)),
            'executed_translated_instructions': [hex(a) for a in sorted(executed & translated)]}

if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--image', type=Path, default=Path('disc/snowblind/BO3NETGM.ELF'))
    parser.add_argument('--output', type=Path, default=Path('work/snowblind/startup-native.json'))
    args = parser.parse_args()
    result = verify(args.image)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(result, indent=2) + '\n')
    print('Verified retail patch parser and no-update path:', result['states'])
