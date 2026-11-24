#!/usr/bin/env python3
"""Check the native StatsRow writer and menu consumer with synthetic counters.

Uses the private US ELF and the same low-word EE translations as the startup
harness. SDK stream, string, cleanup and player-name helpers are hooked; the
row traversal, field order and UI arithmetic execute original instructions.
No game bytes or live account data are exported.
"""
import argparse
import json
import struct
from pathlib import Path

from unicorn import UC_HOOK_CODE
from unicorn.mips_const import (
    UC_MIPS_REG_A0, UC_MIPS_REG_A1, UC_MIPS_REG_A2, UC_MIPS_REG_GP,
    UC_MIPS_REG_PC, UC_MIPS_REG_RA, UC_MIPS_REG_SP, UC_MIPS_REG_V0, UC_MIPS_REG_F12,
)
from verify_snowblind_startup import load_image


def verify(path):
    cpu, translated = load_image(path)
    obj, head, nodes = 0x1000000, 0x1001000, 0x1001100
    output, player, stop, name_helper = 0x1002000, 0x1003000, 0x1100000, 0x1100010
    values = [123.0, 7.0, 9.0, 3.0, 4.0, 2.0]
    trace, executed = [], set()
    writing = True

    def put(address, value):
        cpu.mem_write(address, struct.pack('<I', value))

    def u32(address):
        return struct.unpack('<I', cpu.mem_read(address, 4))[0]

    put(obj + 4, 0x605a00)
    put(obj + 8, player)
    put(obj + 0x14, 1000)  # Writer PID, replaced with the list for the UI run.
    put(obj + 0x1c, head)
    put(head, nodes)
    put(head + 4, nodes + 16 * (len(values) - 1))
    for index, value in enumerate(values):
        address = nodes + 16 * index
        put(address, address + 16 if index + 1 < len(values) else head)
        put(address + 4, address - 16 if index else head)
        cpu.mem_write(address + 8, struct.pack('<f', value))
    put(player + 4, player + 0x100)
    put(player + 0x110, name_helper)

    def hook(machine, address, size, context):
        executed.add(address)
        if address == stop:
            machine.emu_stop()
            return
        a0, a1 = (machine.reg_read(reg) for reg in (UC_MIPS_REG_A0, UC_MIPS_REG_A1))
        result = 0
        if writing and address == 0x2dea88:
            trace.append({'kind': 'string', 'value': 'Synthetic' if a1 == obj + 4 else 'TAG'})
        elif writing and address == 0x2f3980:
            trace.append({'kind': 'u32', 'value': a1})
        elif writing and address == 0x2f39f0:
            bits = machine.reg_read(UC_MIPS_REG_F12)
            trace.append({'kind': 'float32', 'value': struct.unpack('<f', struct.pack('<I', bits))[0]})
        elif not writing and address in {0x3f63d0, 0x3f61f0, 0x558af0, name_helper}:
            pass
        else:
            return
        machine.reg_write(UC_MIPS_REG_V0, result)
        machine.reg_write(UC_MIPS_REG_PC, machine.reg_read(UC_MIPS_REG_RA))

    cpu.hook_add(UC_HOOK_CODE, hook)

    def run(entry, a0, a1, a2=0):
        for register, value in ((UC_MIPS_REG_A0, a0), (UC_MIPS_REG_A1, a1),
                                (UC_MIPS_REG_A2, a2), (UC_MIPS_REG_GP, 0x671d70),
                                (UC_MIPS_REG_SP, 0x1fff000), (UC_MIPS_REG_RA, stop)):
            cpu.reg_write(register, value)
        cpu.emu_start(entry, stop + 4, count=100000)
        assert cpu.reg_read(UC_MIPS_REG_PC) == stop, 'native instruction budget exceeded'

    run(0x15fa70, 0x1004000, obj)
    assert [item['kind'] for item in trace] == ['string', 'string', 'u32', 'u32'] + ['float32'] * 6
    assert [item['value'] for item in trace[2:4]] == [1000, 6]
    assert [item['value'] for item in trace[4:]] == values
    body = bytearray()
    for item in trace:
        if item['kind'] == 'string':
            text = item['value'].encode() + b'\0'
            body += struct.pack('<H', len(text)) + text
        else:
            body += struct.pack('<f' if item['kind'] == 'float32' else '<I', item['value'])

    writing = False
    # Native StatsRow and the UI wrapper both expose their list at +0x14.
    run(0x20a068, output, 0, obj)
    rank = struct.unpack('<i', cpu.mem_read(output, 4))[0]
    ratio = struct.unpack('<f', cpu.mem_read(output + 0x14, 4))[0]
    wins, losses = (u32(output + offset) for offset in (0x1c, 0x20))
    assert (rank, ratio, wins, losses) == (7, 3.0, 4, 2)
    # Exercise the real zero-denominator branch used by a new account.
    cpu.mem_write(nodes + 16 * 1 + 8, struct.pack('<f', -1.0))
    cpu.mem_write(nodes + 16 * 3 + 8, struct.pack('<f', 0.0))
    run(0x20a068, output, 0, obj)
    assert struct.unpack('<i', cpu.mem_read(output, 4))[0] == -1
    assert struct.unpack('<f', cpu.mem_read(output + 0x14, 4))[0] == -9999.0
    return {'kind': 'retail_stats_row_writer_and_menu_with_hooked_sdk_helpers',
            'row_hex': body.hex(), 'trace': trace,
            'menu': {'rank': rank, 'kill_death_ratio': ratio, 'wins': wins, 'losses': losses},
            'new_player': {'rank': -1, 'kill_death_ratio': -9999.0},
            'executed_translated_instructions': [hex(a) for a in sorted(executed & translated)]}


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--image', type=Path, default=Path('disc/snowblind/BO3NETGM.ELF'))
    parser.add_argument('--output', type=Path, default=Path('work/snowblind/stats-native.json'))
    args = parser.parse_args()
    result = verify(args.image)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(result, indent=2) + '\n')
    print('Verified native StatsRow fields, menu K/D and W/L, and unranked zero-death branch')
