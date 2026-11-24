#!/usr/bin/env python3
"""Run native clan, notice and founder codecs with synthetic SDK streams.

The private US ELF supplies the original instructions. SDK primitives and empty
list cleanup are hooked; field order/widths and zero-count traversal are native.
No game bytes or live account data are exported.
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

WRITERS = {0x2dea88: ('string', 0), 0x2f3980: ('u32', 4),
           0x2f39b8: ('u32', 4), 0x2f38a0: ('bool', 1)}
READERS = {0x2deb00: ('string', 0), 0x2f40f0: ('u32', 4),
           0x2f40c0: ('u32', 4), 0x2f3fd0: ('bool', 1)}


def verify(path):
    cpu, translated = load_image(path)
    source, target, head, stop = 0x1000000, 0x1001000, 0x1002000, 0x1100000
    strings = {'TAG': 0x1003000, 'Synthetic Clan': 0x1003100,
               'Founder': 0x1003200, '': 0x1003300, 'Synthetic notice': 0x1003400, 'Second': 0x1003500}
    for text, address in strings.items():
        cpu.mem_write(address, text.encode() + b'\0')

    def put(address, value):
        cpu.mem_write(address, struct.pack('<I', value))

    def get(address):
        return struct.unpack('<I', cpu.mem_read(address, 4))[0]

    traces, bodies, executed = {}, {}, set()
    trace, reading, position = [], False, 0

    def hook(machine, address, size, context):
        nonlocal position
        executed.add(address)
        if address == stop:
            machine.emu_stop()
            return
        if reading and address in {0x544c50, 0x515508}:
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
            if width:
                value = argument & ((1 << (width * 8)) - 1)
            else:
                value = bytes(cpu.mem_read(get(argument), 64)).split(b'\0', 1)[0].decode()
            trace.append({'kind': kind, 'value': value})
        machine.reg_write(UC_MIPS_REG_V0, 1)
        machine.reg_write(UC_MIPS_REG_PC, machine.reg_read(UC_MIPS_REG_RA))

    cpu.hook_add(UC_HOOK_CODE, hook)
    for name, writer, reader, fields in (
        ('description', 0x173950, 0x173a10, {4: strings['TAG'], 12: strings['Synthetic Clan'], 20: 0xffffffff}),
        ('alerts', 0x173e48, 0x173eb0, {4: strings[''], 12: strings['']}),
        ('founder', 0x173ba0, 0x173ca0, {4: strings['Founder'], 12: strings['']}),
        ('online_founder', 0x173ba0, 0x173ca0, {4: strings['Founder'], 12: strings['']}),
        ('officer', 0x173ba0, 0x173ca0, {4: strings['Second'], 12: strings['']}),
        ('member', 0x173ba0, 0x173ca0, {4: strings['Second'], 12: strings['']}),
        ('invitation', 0x173f10, 0x173fa0, {4: strings['TAG'], 12: strings['Synthetic Clan'],
                                         20: strings['Founder'], 28: 0xffffffff, 32: 1, 36: strings['Second']}),
        ('notice', 0x174028, 0x174068, {4: strings['Founder'], 12: strings['Synthetic notice']}),
    ):
        cpu.mem_write(source, b'\0' * 0x30)
        cpu.mem_write(target, b'\0' * 0x30)
        put(head, head)
        put(head + 4, head)
        put(source + 0x1c, head)
        put(target + 0x1c, head)
        for offset, value in fields.items():
            put(source + offset, value)
        if name in {'founder', 'online_founder', 'member', 'officer'}:
            cpu.mem_write(source + 0x14, bytes([name in {'founder', 'online_founder'}, name == 'officer', 1]))
            cpu.mem_write(source + 0x24, bytes([name == 'online_founder']))
        if name == 'invitation':
            cpu.mem_write(source + 0x2c, b'\1')
        trace, reading, position = [], False, 0
        for entry, record in ((writer, source), (reader, target)):
            for register, value in ((UC_MIPS_REG_A0, 0x1004000), (UC_MIPS_REG_A1, record),
                                    (UC_MIPS_REG_GP, 0x671d70), (UC_MIPS_REG_SP, 0x1fff000),
                                    (UC_MIPS_REG_RA, stop)):
                cpu.reg_write(register, value)
            cpu.emu_start(entry, stop + 4, count=100000)
            assert cpu.reg_read(UC_MIPS_REG_PC) == stop, 'native codec exceeded budget'
            reading = True
        assert position == len(trace)
        for offset in fields:
            assert get(source + offset) == get(target + offset)
        if name in {'founder', 'online_founder', 'member', 'officer'}:
            assert bytes(cpu.mem_read(target + 0x14, 3)) == bytes([name in {'founder', 'online_founder'}, name == 'officer', 1])
            assert bytes(cpu.mem_read(target + 0x24, 1)) == bytes([name == 'online_founder'])
        if name == 'invitation':
            assert bytes(cpu.mem_read(target + 0x2c, 1)) == b'\1'
        body = bytearray()
        for item in trace:
            if item['kind'] == 'string':
                text = item['value'].encode() + b'\0'
                body += struct.pack('<H', len(text)) + text
            else:
                body += item['value'].to_bytes(1 if item['kind'] == 'bool' else 4, 'little')
        traces[name], bodies[name] = trace, bytes(body)
    assert [x['kind'] for x in traces['description']] == ['string', 'string', 'u32', 'u32']
    assert [x['kind'] for x in traces['alerts']] == ['bool'] * 3 + ['string'] * 2
    assert [x['kind'] for x in traces['founder']] == ['string'] * 2 + ['bool'] * 3 + ['u32', 'bool']
    assert [x['kind'] for x in traces['invitation']] == ['string'] * 3 + ['u32'] * 2 + ['string', 'bool']
    invitations_reply = struct.pack('<II', 0, 1) + bodies['invitation'] + struct.pack('<I', 0) + bodies['member']
    reply = struct.pack('<I', 0) + bodies['description'] + bodies['alerts'] + struct.pack('<I', 1) + bodies['founder'] * 2
    online_reply = struct.pack('<I', 0) + bodies['description'] + bodies['alerts'] + struct.pack('<I', 1) + bodies['online_founder'] * 2
    promoted_reply = struct.pack('<I', 0) + bodies['description'] + bodies['alerts'] + struct.pack('<I', 2) + bodies['founder'] + bodies['officer'] * 2
    notice_reply = (struct.pack('<I', 0) + bodies['description'][:-4] + struct.pack('<I', 1)
                    + bodies['notice'] + bodies['alerts'] + struct.pack('<I', 1) + bodies['founder'] * 2)
    return {'kind': 'retail_clan_codecs_with_hooked_sdk_primitives_and_empty_list_cleanup',
            'create_args_hex': bodies['description'].hex(), 'fetch_my_clan_reply_hex': reply.hex(),
            'online_fetch_my_clan_reply_hex': online_reply.hex(),
            'notice_fetch_my_clan_reply_hex': notice_reply.hex(),
            'check_my_invitations_reply_hex': invitations_reply.hex(),
            'officer_member_hex': bodies['officer'].hex(),
            'promoted_fetch_my_clan_reply_hex': promoted_reply.hex(),
            'reader_matches': True, 'trace': traces,
            'executed_translated_instructions': [hex(a) for a in sorted(executed & translated)]}


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--image', type=Path, default=Path('disc/snowblind/BO3NETGM.ELF'))
    parser.add_argument('--output', type=Path, default=Path('work/snowblind/clans-native.json'))
    args = parser.parse_args()
    result = verify(args.image)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(result, indent=2) + '\n')
    print('Verified native clan description, notice, alert, member and invitation writer/reader codecs')
