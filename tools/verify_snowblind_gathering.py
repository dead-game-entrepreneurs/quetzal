#!/usr/bin/env python3
"""Trace native BO3Match field codecs using synthetic values and hooked SDK I/O.

Verifies native field order, widths and reader symmetry. Any/Data envelopes and
transport framing are outside this check. No game bytes or live data exported.
"""
import argparse
import json
import struct
from pathlib import Path
from unicorn import UC_HOOK_CODE
from unicorn.mips_const import (
    UC_MIPS_REG_A0, UC_MIPS_REG_A1, UC_MIPS_REG_GP, UC_MIPS_REG_PC,
    UC_MIPS_REG_RA, UC_MIPS_REG_SP, UC_MIPS_REG_V0,
)
from verify_snowblind_startup import load_image

WRITERS = {0x2f3980: ('u32', 4), 0x2f3910: ('u16', 2),
           0x2f3868: ('u8', 1), 0x2dea88: ('string', None)}
READERS = {0x2f40f0: ('u32', 4), 0x2f4060: ('u16', 2),
           0x2f4000: ('u8', 1), 0x2deb00: ('string', None)}


def verify(path):
    cpu, translated = load_image(path)
    original, restored, text, stream, stop = 0x1000000, 0x1001000, 0x1002000, 0x1003000, 0x1100000
    cpu.mem_write(text, b'Synthetic room\0')
    record = bytearray(0x48)
    for offset, value in ((4, 10000), (8, 1000), (12, 1000), (20, 7),
                          (24, 0), (28, 16), (32, 2), (36, text), (68, 300)):
        struct.pack_into('<I', record, offset, value)
    struct.pack_into('<HH', record, 16, 1, 8)
    record[60:65] = bytes((1, 2, 0x82, 0x31, 3))
    cpu.mem_write(original, bytes(record))
    trace, executed = [], set()
    reading, position = False, 0
    def hook(machine, address, size, context):
        nonlocal position
        executed.add(address)
        if address == stop:
            machine.emu_stop()
            return
        primitives = READERS if reading else WRITERS
        if address not in primitives:
            return
        kind, width = primitives[address]
        argument = machine.reg_read(UC_MIPS_REG_A1)
        if reading:
            item = trace[position]
            if item['kind'] != kind:
                raise AssertionError('reader field order differs')
            value = item['value']
            machine.mem_write(argument, value.to_bytes(width, 'little') if width else struct.pack('<I', text))
            position += 1
        else:
            value = argument & ((1 << (width * 8)) - 1) if width else 'Synthetic room'
            if not width:
                pointer = struct.unpack('<I', machine.mem_read(argument, 4))[0]
                assert pointer == text
            trace.append({'kind': kind, 'value': value})
        machine.reg_write(UC_MIPS_REG_V0, 1)
        machine.reg_write(UC_MIPS_REG_PC, machine.reg_read(UC_MIPS_REG_RA))
    cpu.hook_add(UC_HOOK_CODE, hook)
    for entry, destination in ((0x15dd10, original), (0x15dd88, restored)):
        for register, value in ((UC_MIPS_REG_A0, stream), (UC_MIPS_REG_A1, destination),
                                (UC_MIPS_REG_SP, 0x1fff000), (UC_MIPS_REG_GP, 0x671d70),
                                (UC_MIPS_REG_RA, stop)):
            cpu.reg_write(register, value)
        cpu.emu_start(entry, stop + 4, count=100000)
        if cpu.reg_read(UC_MIPS_REG_PC) != stop:
            raise AssertionError('native codec exceeded instruction budget')
        reading = True
    expected = ['u32'] * 3 + ['u16'] * 2 + ['u32'] * 4 + ['string'] + ['u8'] * 5 + ['u32']
    assert [item['kind'] for item in trace] == expected and position == len(trace)
    assert bytes(cpu.mem_read(restored, len(record))) == record
    body = bytearray()
    for item in trace:
        if item['kind'] == 'string':
            value = item['value'].encode() + b'\0'
            body += struct.pack('<H', len(value)) + value
        else:
            body += item['value'].to_bytes({'u32': 4, 'u16': 2, 'u8': 1}[item['kind']], 'little')
    return {'kind': 'retail_BO3Match_field_codecs_with_hooked_stream_primitives',
            'reader_matches': True, 'field_body_hex': body.hex(), 'trace': trace,
            'executed_translated_instructions': [hex(a) for a in sorted(executed & translated)]}

if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--image', type=Path, default=Path('disc/snowblind/BO3NETGM.ELF'))
    parser.add_argument('--output', type=Path, default=Path('work/snowblind/gathering-native.json'))
    args = parser.parse_args()
    result = verify(args.image)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(result, indent=2) + '\n')
    print('Verified native BO3Match writer/reader:', len(result['trace']), 'fields')
