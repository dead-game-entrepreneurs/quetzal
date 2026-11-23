#!/usr/bin/env python3
"""Trace retail Commandos gathering codecs and verify its PRUDP checksum.

Requires Unicorn and the extracted US FIN_RED/RVOUS images. Stream primitives
are hooked: this checks native field order, widths, values and reader symmetry,
not full BitStream execution, transport framing or a retail server connection.
Only low-word EE register saves/loads and pointer arithmetic are translated for
MIPS32 execution. Game binaries and the resulting traces stay in ignored paths.
"""
import argparse
import hashlib
import json
import struct
from pathlib import Path

from unicorn import Uc, UC_ARCH_MIPS, UC_HOOK_CODE, UC_MODE_LITTLE_ENDIAN, UC_MODE_MIPS32
from unicorn.mips_const import (
    UC_MIPS_REG_A0, UC_MIPS_REG_A1, UC_MIPS_REG_A2, UC_MIPS_REG_PC, UC_MIPS_REG_RA,
    UC_MIPS_REG_SP, UC_MIPS_REG_V0,
)

IMAGES = (
    ("FIN_RED.BIN", 0x151E80, 0x603850, "25f2d3ffb7f9ceb54635f7ac69bb8afb510a37e2c2cf6cad2e5243e5ef484e60"),
    ("RVOUS_VF.BIN", 0x8C0480, 0x22280, "6d4b041b4ad896eebaa6ba8ff5eeb001cb852feadde64cd62baa116286648205"),
)
WRITERS = {0x675780: ("u32", 4), 0x675740: ("u16", 2),
           0x675700: ("u8", 1), 0x6756C0: ("bool", 1), 0x6956B0: ("string", None)}
READERS = {0x6760B0: ("u32", 4), 0x675FF0: ("u16", 2),
           0x675FB0: ("u8", 1), 0x675F30: ("bool", 1), 0x695750: ("string", None)}


def verify(disc):
    machine = Uc(UC_ARCH_MIPS, UC_MODE_MIPS32 | UC_MODE_LITTLE_ENDIAN)
    machine.mem_map(0, 0x2000000)
    patches = set()
    for name, base, text_end, expected in IMAGES:
        data = bytearray((disc / name).read_bytes())
        if hashlib.sha256(data).hexdigest() != expected:
            raise ValueError(f"unsupported executable hash: {name}")
        for offset in range(64, text_end, 4):
            word = struct.unpack_from("<I", data, offset)[0]
            opcode, replacement = word >> 26, None
            if opcode in (0x1E, 0x1F, 0x37, 0x3F):  # LQ/SQ/LD/SD -> LW/SW
                replacement = (word & 0x3FFFFFF) | ((0x23 if opcode in (0x1E, 0x37) else 0x2B) << 26)
            if opcode == 0 and word & 0x3F in (0x2D, 0x2F):  # DADDU/DSUBU -> ADDU/SUBU
                replacement = (word & ~0x3F) | (0x21 if word & 0x3F == 0x2D else 0x23)
            if replacement is not None:
                struct.pack_into("<I", data, offset, replacement)
                patches.add(base + offset)
        machine.mem_write(base, bytes(data))

    stream, original, restored, text, stop = 0x1000000, 0x1001000, 0x1001800, 0x1002000, 0x1100000
    machine.mem_write(text, b"Synthetic room\0")
    record = bytearray(600)
    for offset, value in ((4, 0x01020304), (8, 0x11121314), (12, 0x21222324),
                          (20, 7), (24, 0), (28, 16), (32, 2), (36, text), (596, 0x10203)):
        struct.pack_into("<I", record, offset, value)
    struct.pack_into("<HH", record, 16, 1, 8)
    record[48:52] = bytes((2, 3, 1, 0))
    record[52:63] = b"arena-test\0"
    record[84:103] = b"mode=2;map=ST03_MP\0"
    machine.mem_write(original, bytes(record))
    trace, executed = [], set()
    reading, position = False, 0

    def hook(cpu, address, size, context):
        nonlocal position
        executed.add(address)
        if address == stop:
            cpu.emu_stop()
            return
        primitives = READERS if reading else WRITERS
        if address not in primitives:
            return
        kind, width = primitives[address]
        argument = cpu.reg_read(UC_MIPS_REG_A1)
        if reading:
            item = trace[position]
            if item["kind"] != kind:
                raise AssertionError(f"reader disagrees at primitive {position}: {kind}")
            if width:
                cpu.mem_write(argument, item["value"].to_bytes(width, "little"))
            else:
                cpu.mem_write(argument, struct.pack("<I", text))
            position += 1
        else:
            value = argument
            if width:
                value &= (1 << (8 * width)) - 1
            else:
                pointer = struct.unpack("<I", cpu.mem_read(argument, 4))[0]
                value = bytes(cpu.mem_read(pointer, 128)).split(b"\0", 1)[0].decode("ascii")
            trace.append({"kind": kind, "value": value})
        cpu.reg_write(UC_MIPS_REG_V0, 1)
        cpu.reg_write(UC_MIPS_REG_PC, cpu.reg_read(UC_MIPS_REG_RA))

    machine.hook_add(UC_HOOK_CODE, hook)
    for entry, destination in ((0x45FB40, original), (0x45FC00, restored)):
        machine.reg_write(UC_MIPS_REG_A0, stream)
        machine.reg_write(UC_MIPS_REG_A1, destination)
        machine.reg_write(UC_MIPS_REG_SP, 0x1FFF000)
        machine.reg_write(UC_MIPS_REG_RA, stop)
        machine.emu_start(entry, stop + 4, count=100000)
        if machine.reg_read(UC_MIPS_REG_PC) != stop:
            raise AssertionError("native codec exceeded the instruction budget")
        reading = True
    expected_kinds = (["u32"] * 3 + ["u16"] * 2 + ["u32"] * 4 + ["string"] +
                      ["u8"] * 2 + ["bool"] * 2 + ["u8"] * 544 + ["u32"])
    if [item["kind"] for item in trace] != expected_kinds or position != len(trace):
        raise AssertionError("gathering primitive layout changed")
    if bytes(machine.mem_read(restored, 600)) != record:
        actual = bytes(machine.mem_read(restored, 600))
        differences = [(i, a, b) for i, (a, b) in enumerate(zip(actual, record)) if a != b]
        raise AssertionError(f"native reader did not restore the synthetic record: {differences[:8]}")
    checksum_checks = 0
    for length in (*range(66), 127, 128, 129, 255, 256, 257, 1024):
        payload = bytes((i * 73 + length * 19) & 255 for i in range(length))
        machine.mem_write(0x1003000, payload or b"\0")
        machine.mem_write(0x1004008, struct.pack("<I", 0x1003000))
        for seed in (0, 0xA6, 0xBE, 0xFF):
            machine.reg_write(UC_MIPS_REG_A0, 0x1004000)
            machine.reg_write(UC_MIPS_REG_A1, length)
            machine.reg_write(UC_MIPS_REG_A2, seed)
            machine.reg_write(UC_MIPS_REG_SP, 0x1FFF000)
            machine.reg_write(UC_MIPS_REG_RA, stop)
            machine.emu_start(0x68D6F0, stop + 4, count=100000)
            boundary = length - length % 4
            words = sum(int.from_bytes(payload[i:i + 4], "little") for i in range(0, boundary, 4)) & 0xFFFFFFFF
            expected = (seed + sum(words.to_bytes(4, "little")) + sum(payload[boundary:])) & 255
            if machine.reg_read(UC_MIPS_REG_PC) != stop or machine.reg_read(UC_MIPS_REG_V0) & 255 != expected:
                raise AssertionError(f"native checksum mismatch: length={length}, seed={seed}")
            checksum_checks += 1
    body = bytearray()
    for item in trace:
        if item["kind"] == "string":
            value = item["value"].encode("ascii") + b"\0"
            body += struct.pack("<H", len(value)) + value
        else:
            width = {"u32": 4, "u16": 2, "u8": 1, "bool": 1}[item["kind"]]
            body += item["value"].to_bytes(width, "little")
    return {"kind": "retail_serializer_trace_with_hooked_stream_primitives", "reader_matches": True,
            "field_body_hex": body.hex(),
            "primitive_calls_per_codec": len(trace), "checksum_checks": checksum_checks, "trace": trace,
            "executed_translated_instructions": [hex(a) for a in sorted(executed & patches)]}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--disc", type=Path, default=Path("disc/commandos"))
    parser.add_argument("--output", type=Path, default=Path("work/commandos/gathering-native.json"))
    args = parser.parse_args()
    result = verify(args.disc)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(result, indent=2) + "\n")
    print(f"Verified both codecs: {result['primitive_calls_per_codec']} primitives each; "
          f"{result['checksum_checks']} native checksum cases; {args.output}")


if __name__ == "__main__":
    main()
