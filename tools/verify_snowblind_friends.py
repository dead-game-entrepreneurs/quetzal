#!/usr/bin/env python3
"""Trace the native Snowblind friend-row codecs with synthetic SDK streams.

The private US ELF supplies original instructions. SDK scalar/string primitives
and empty gathering-list cleanup are hooked. No live names or game bytes export.
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

WRITERS = {0x2f3980: ('u32', 4), 0x2f3868: ('u8', 1), 0x2dea88: ('string', 0)}
READERS = {0x2f40f0: ('u32', 4), 0x2f4000: ('u8', 1), 0x2deb00: ('string', 0)}


def verify(path):
    cpu, translated = load_image(path)
    source, target, head, stop = 0x1000000, 0x1001000, 0x1002000, 0x1100000
    strings = {'Second': 0x1003000, 'TAG': 0x1003100, '': 0x1003200, '||': 0x1003300}
    for text, address in strings.items():
        cpu.mem_write(address, text.encode() + b'\0')
    def put(address, value):
        cpu.mem_write(address, struct.pack('<I', value))
    def get(address):
        return struct.unpack('<I', cpu.mem_read(address, 4))[0]
    rows, traces, alerts, executed = {}, {}, {}, set()
    trace, position, reading = [], 0, False
    alert_mode = False
    def hook(machine, address, size, context):
        nonlocal position
        executed.add(address)
        if address == stop:
            machine.emu_stop()
            return
        if alert_mode and address in {0x515028, 0x5153b0, 0x5150c0}:
            if address == 0x5153b0:
                put(machine.reg_read(UC_MIPS_REG_A0) + 8, head)
            machine.reg_write(UC_MIPS_REG_PC, machine.reg_read(UC_MIPS_REG_RA))
            return
        if reading and address == 0x515508:
            machine.reg_write(UC_MIPS_REG_PC, machine.reg_read(UC_MIPS_REG_RA))
            return
        primitive = (READERS if reading else WRITERS).get(address)
        if primitive is None:
            return
        kind, width = primitive
        argument = machine.reg_read(UC_MIPS_REG_A1)
        if reading:
            item = trace[position]
            assert item['kind'] == kind
            value = item['value']
            cpu.mem_write(argument, value.to_bytes(width, 'little') if width else struct.pack('<I', strings[value]))
            position += 1
        else:
            value = argument & ((1 << (width * 8)) - 1) if width else bytes(cpu.mem_read(get(argument), 64)).split(b'\0', 1)[0].decode()
            trace.append({'kind': kind, 'value': value})
        machine.reg_write(UC_MIPS_REG_V0, 1)
        machine.reg_write(UC_MIPS_REG_PC, machine.reg_read(UC_MIPS_REG_RA))
    cpu.hook_add(UC_HOOK_CODE, hook)
    for relation in range(3):
        for online in (False, True):
            for record in (source, target):
                cpu.mem_write(record, b'\0' * 0x30)
                put(record + 0x28, head)
            put(head, head)
            put(head + 4, head)
            for offset, value in ((4, 1001), (8, strings['Second']),
                                  (20, strings['||' if online else '']), (28, strings['TAG'])):
                put(source + offset, value)
            cpu.mem_write(source + 16, bytes([relation]))
            trace, position, reading = [], 0, False
            for entry, record in ((0x15fe28, source), (0x15ff10, target)):
                for register, value in ((UC_MIPS_REG_A0, 0x1004000), (UC_MIPS_REG_A1, record),
                                        (UC_MIPS_REG_GP, 0x671d70), (UC_MIPS_REG_SP, 0x1fff000),
                                        (UC_MIPS_REG_RA, stop)):
                    cpu.reg_write(register, value)
                cpu.emu_start(entry, stop + 4, count=100000)
                assert cpu.reg_read(UC_MIPS_REG_PC) == stop
                reading = True
            assert position == len(trace)
            assert [x['kind'] for x in trace] == ['u32', 'string', 'u8', 'string', 'string', 'u32']
            assert bytes(cpu.mem_read(source + 4, 0x2c)) == bytes(cpu.mem_read(target + 4, 0x2c))
            body = bytearray()
            for item in trace:
                if item['kind'] == 'string':
                    text = item['value'].encode() + b'\0'
                    body += struct.pack('<H', len(text)) + text
                else:
                    body += item['value'].to_bytes(1 if item['kind'] == 'u8' else 4, 'little')
            key = f'{relation}_{"online" if online else "offline"}'
            rows[key], traces[key] = (struct.pack('<I', 1) + body).hex(), trace
    # Execute the real alert predicate and relation getter. Only SDK list
    # construction/copy/destruction is hooked for this synthetic one-row list.
    alert_mode = True
    root, node, player = 0x1005000, 0x1006000, 0x1007000
    put(root + 0x5c, root + 0x100)
    put(head, node)
    put(node, head)
    put(node + 8, player)
    put(player + 4, 0x605570)
    for relation in range(3):
        put(player + 0x3c, relation)
        for register, value in ((UC_MIPS_REG_A0, root), (UC_MIPS_REG_GP, 0x671d70),
                                (UC_MIPS_REG_SP, 0x1fff000), (UC_MIPS_REG_RA, stop)):
            cpu.reg_write(register, value)
        cpu.emu_start(0x223638, stop + 4, count=100000)
        assert cpu.reg_read(UC_MIPS_REG_PC) == stop
        alerts[str(relation)] = bool(cpu.reg_read(UC_MIPS_REG_V0))
    assert alerts == {'0': True, '1': False, '2': False}
    return {'kind': 'retail_friend_row_codecs_with_hooked_sdk_primitives',
            'reader_matches': True, 'query_replies_hex': rows, 'trace': traces,
            'incoming_alert_by_relationship': alerts,
            'executed_translated_instructions': [hex(a) for a in sorted(executed & translated)]}


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--image', type=Path, default=Path('disc/snowblind/BO3NETGM.ELF'))
    parser.add_argument('--output', type=Path, default=Path('work/snowblind/friends-native.json'))
    args = parser.parse_args()
    result = verify(args.image)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(result, indent=2) + '\n')
    print('Verified six native friend-row codecs and the incoming-only notification predicate')
