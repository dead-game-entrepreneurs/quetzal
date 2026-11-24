#!/usr/bin/env python3
"""Execute native Snowblind NAT payload writer/reader with synthetic SDK hooks.

The private US executable supplies the original instructions. Address, buffer,
clock and send helpers are hooked; no executable bytes or live data are exported.
This verifies the 13-byte payload, not native UDP checksum or fallback timing.
"""
import argparse
import json
import struct
from pathlib import Path

from unicorn import UC_HOOK_CODE
from unicorn.mips_const import (
    UC_MIPS_REG_A0, UC_MIPS_REG_A1, UC_MIPS_REG_A2, UC_MIPS_REG_A3,
    UC_MIPS_REG_T0, UC_MIPS_REG_GP, UC_MIPS_REG_PC, UC_MIPS_REG_RA,
    UC_MIPS_REG_SP, UC_MIPS_REG_V0,
)
from verify_snowblind_startup import load_image


def verify(path):
    cpu, translated = load_image(path)
    stream, buffer, vtable, stamp = 0x1000000, 0x1001000, 0x1002000, 0x1003000
    stop, send, release = 0x1100000, 0x1100100, 0x1100200
    def put(address, value):
        cpu.mem_write(address, struct.pack('<I', value))
    put(stream + 8, vtable)
    put(vtable + 0x18, send)
    put(buffer + 4, vtable + 0x100)
    put(vtable + 0x110, release)
    put(stream + 0x18, stream + 0x100)
    # Low-word EE translation: synthetic timestamp deliberately fits 32 bits.
    nonce = struct.pack('<Q', 0x11223344)
    body, writes, reads, decoded, executed = bytearray(), [], [], [], set()
    position = 0

    def hook(machine, address, size, context):
        nonlocal position
        executed.add(address)
        if address == stop:
            machine.emu_stop()
            return
        a0, a1, a2, a3 = [machine.reg_read(r) for r in
                          (UC_MIPS_REG_A0, UC_MIPS_REG_A1, UC_MIPS_REG_A2, UC_MIPS_REG_A3)]
        result = 1
        if address in {0x3162f0, 0x316680, 0x3166d0, 0x316330, send, release}:
            pass
        elif address in {0x302ed0, 0x2d9550}:
            result = buffer
        elif address == 0x31aee0:
            result = 0x11223344
        elif address == 0x2d9678:
            writes.append(a2)
            body.extend(cpu.mem_read(a1, a2))
        elif address == 0x2d9940:
            result = len(body)
        elif address == 0x2d9728:
            reads.append((a2, a3))
            assert position == a3
            cpu.mem_write(a1, bytes(body[position:position+a2]))
            position += a2
        elif address == 0x35ba40:
            timestamp = machine.reg_read(UC_MIPS_REG_T0)
            decoded.append((a1, a3, bytes(cpu.mem_read(timestamp+8, 8)).hex()))
        else:
            return
        machine.reg_write(UC_MIPS_REG_V0, result)
        machine.reg_write(UC_MIPS_REG_PC, machine.reg_read(UC_MIPS_REG_RA))

    cpu.hook_add(UC_HOOK_CODE, hook)
    for entry, registers in (
        (0x35c6a0, ((UC_MIPS_REG_A0, stream), (UC_MIPS_REG_A1, 0),
                    (UC_MIPS_REG_A2, stamp), (UC_MIPS_REG_A3, 7), (UC_MIPS_REG_T0, stamp))),
        (0x35c7d0, ((UC_MIPS_REG_A0, stream), (UC_MIPS_REG_A1, stamp),
                    (UC_MIPS_REG_A2, buffer))),
    ):
        for register, value in registers + ((UC_MIPS_REG_GP, 0x671d70),
                                            (UC_MIPS_REG_SP, 0x1fff000), (UC_MIPS_REG_RA, stop)):
            cpu.reg_write(register, value)
        cpu.emu_start(entry, stop+4, count=100000)
        assert cpu.reg_read(UC_MIPS_REG_PC) == stop, 'native routine exceeded budget'
    expected = b'\0' + struct.pack('<I', 7) + nonce
    assert body == expected
    assert writes == [1, 4, 8]
    assert reads == [(1, 0), (4, 1), (8, 5)]
    assert decoded == [(0, 7, nonce.hex())]
    return {'kind': 'retail_nat_payload_with_hooked_sdk_helpers',
            'writer': '0x35c6a0', 'reader': '0x35c7d0',
            'payload_hex': body.hex(), 'write_widths': writes,
            'read_width_offsets': reads, 'reader_matches': True,
            'executed_translated_instructions': [hex(a) for a in sorted(executed & translated)]}


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--image', type=Path, default=Path('disc/snowblind/BO3NETGM.ELF'))
    parser.add_argument('--output', type=Path, default=Path('work/snowblind/nat-native.json'))
    args = parser.parse_args()
    result = verify(args.image)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(result, indent=2) + '\n')
    print('Verified native 13-byte NAT payload writer/reader: kind, RVCID, timing echo')
