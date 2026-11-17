#!/usr/bin/env python3
"""Prepare a plain-symbol SNR2 module for Ghidra (adapted from Angelware's workflow).

25 to Life PATCHLIB uses the same header/relocations as Angelware's SNR2
modules, but its symbol names are plain GCC names rather than obfuscated names.
Only an analysis copy is changed; imports point at named synthetic stubs.
"""
import argparse
import os
import struct
import subprocess
from pathlib import Path

EXTERN_BASE = 0x02000000


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("module", type=Path)
    parser.add_argument("output", type=Path)
    parser.add_argument("--text-end", type=lambda x: int(x, 0), required=True)
    args = parser.parse_args()
    data = bytearray(args.module.read_bytes())
    if data[:4] != b"SNR2":
        parser.error("expected an SNR2 module")
    reloc, nreloc, symtab, nsym = struct.unpack_from("<IIII", data, 4)
    if reloc + nreloc * 12 > len(data) or symtab + nsym * 12 > len(data):
        parser.error("invalid SNR2 table bounds")
    entry, ctors = struct.unpack_from("<II", data, 0x18)
    rows = [(entry, "func", "snr2_entry", ""), (ctors, "func", "snr2_run_ctors", "")]
    imports = {}
    for i in range(nsym):
        name_offset, value, _ = struct.unpack_from("<III", data, symtab + i * 12)
        if not name_offset:
            continue
        name = bytes(data[name_offset:data.index(0, name_offset)]).decode("ascii")
        if not name:
            continue
        if value:
            kind = "func" if value < args.text_end else "data"
            rows.append((value, kind, name, ""))
        else:
            imports[i] = EXTERN_BASE + len(imports) * 8
            rows.append((imports[i], "extern", name, ""))
    for i in range(nreloc):
        offset, info, addend = struct.unpack_from("<III", data, reloc + i * 12)
        kind, symbol = info & 255, info >> 8
        if symbol not in imports:
            raise ValueError(f"relocation targets a non-import symbol {symbol}")
        target = imports[symbol] + addend
        insn, = struct.unpack_from("<I", data, offset)
        if kind == 2:
            insn = target & 0xffffffff
        elif kind == 4:
            insn = (insn & 0xfc000000) | ((target >> 2) & 0x3ffffff)
        elif kind == 5:
            insn = (insn & 0xffff0000) | (((target + 0x8000) >> 16) & 0xffff)
        elif kind == 6:
            insn = (insn & 0xffff0000) | (target & 0xffff)
        else:
            raise ValueError(f"unsupported relocation type {kind}")
        struct.pack_into("<I", data, offset, insn)
    ghidra_install = Path(os.environ.get("GHIDRA_INSTALL_DIR", Path.home() / ".local/opt/ghidra_12.1.4_PUBLIC"))
    demangler = ghidra_install / "GPL/DemanglerGnu/os/linux_x86_64/demangler_gnu_v2_24"
    result = subprocess.run([str(demangler), "-s", "gnu"],
                            input="\n".join(row[2] for row in rows) + "\n",
                            text=True, capture_output=True, check=True)
    pretty = result.stdout.splitlines()
    if len(pretty) != len(rows):
        raise ValueError("unexpected demangler output length")
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.with_suffix(".bin").write_bytes(data)
    with args.output.with_suffix(".syms.tsv").open("w") as out:
        for (address, kind, name, _), nice in sorted(zip(rows, pretty)):
            out.write(f"{address:#x}\t{kind}\t{name}\t{nice}\t\n")
    print(f"{len(rows)} symbols, {len(imports)} imports, {nreloc} relocated references")


if __name__ == "__main__":
    main()
