#!/usr/bin/env python3
"""Read-only audit of updater/menu linkage in the researched US PS2 release.

Reads a locally supplied retail ELF and MAIN.HOG. Prints observations as JSON;
does not modify, extract, download, install, or serve game/update files.
"""
import argparse
import hashlib
import json
import struct
from pathlib import Path

from hog_extract import entries


ELF_SHA256 = "52963efa0a12a44db01dbfad65d9bbb865c802c7ddf4513855913314b69cdba3"


def checked_read(data, address, size):
    if address < 0 or size < 0 or address + size > len(data):
        raise ValueError("read outside file-backed data")
    return data[address:address + size]


def cstring(reader, address):
    result = bytearray()
    for i in range(512):
        char = reader(address + i, 1)
        if char == b"\0":
            return result.decode("ascii")
        result.extend(char)
    raise ValueError("unterminated symbol name")


def symbols(reader, header):
    if reader(header, 4) != b"SNR2":
        raise ValueError("expected an SNR2 header")
    table, count = struct.unpack("<II", reader(header + 12, 8))
    if count > 10000:
        raise ValueError("unexpected symbol count")
    result = []
    for i in range(count):
        name, value, flags = struct.unpack("<III", reader(table + i * 12, 12))
        if name:
            text = cstring(reader, name)
            if text:
                result.append((text, value, (flags >> 16) & 255))
    return result


def inspect(elf, hog):
    data = elf.read_bytes()
    digest = hashlib.sha256(data).hexdigest()
    if digest != ELF_SHA256:
        raise ValueError("ELF differs from the researched SLUS-210.16 release; addresses do not apply")
    header = struct.unpack_from("<16sHHIIIIIHHHHHH", data)
    segments = []
    for i in range(header[10]):
        kind, offset, address, _, size, _, _, _ = struct.unpack_from(
            "<8I", data, header[5] + i * header[9])
        if kind == 1:
            segments.append((address, size, offset))

    def read(address, size):
        for base, length, offset in segments:
            if base <= address and address + size <= base + length:
                return checked_read(data, offset + address - base, size)
        raise ValueError("address is outside file-backed ELF segments")

    def word(address):
        return struct.unpack("<I", read(address, 4))[0]

    # The native module registry starts with the main executable's SNR2 record.
    main_record = word(0x58a87c)
    main_symbols = symbols(read, main_record)
    relocations, relocation_count = struct.unpack("<II", read(main_record + 4, 8))
    relocation_sites = [word(relocations + i * 12) for i in range(relocation_count)]

    # Search direct JAL instructions in the main .text section only.
    sections = [struct.unpack_from("<10I", data, header[6] + i * header[11])
                for i in range(header[12])]
    names = sections[header[13]]
    name_data = checked_read(data, names[4], names[5])
    calls = {0x430fd8: [], 0x2738e0: []}
    for section in sections:
        name = name_data[section[0]:].split(b"\0", 1)[0]
        if name != b".text":
            continue
        code = checked_read(data, section[4], section[5])
        for i, (instruction,) in enumerate(struct.iter_unpack("<I", code)):
            if instruction >> 26 != 3:
                continue
            pc = section[3] + i * 4
            target = ((pc + 4) & 0xf0000000) | ((instruction & 0x3ffffff) << 2)
            if target in calls:
                calls[target].append(f"0x{pc:08x}")

    commands = []
    for i in range(10):
        _, callback, name, _ = struct.unpack("<4I", read(0x5a5a58 + i * 16, 16))
        commands.append({"name": cstring(read, name), "callback": f"0x{callback:08x}"})

    modules = []
    with hog.open("rb") as stream:
        for name, offset, size, _ in entries(stream):
            if not name.upper().endswith(".REL"):
                continue
            stream.seek(offset)
            module = stream.read(size)
            rows = symbols(lambda a, n: checked_read(module, a, n), 0)
            imports = [text for text, _, kind in rows if kind == 1]
            modules.append({
                "name": name,
                "imports_ee_loader": "snDllLoaded" in imports,
                "imports_extractor_module_loader": [x for x in imports if x.startswith("LoadModule__14PatchExtractor")],
            })

    return {
        "target": "SLUS-210.16",
        "elf_sha256": digest,
        "main_linkage_entry_count": word(main_record + 16),
        "main_named_linkage_symbol_count": len(main_symbols),
        "menu_or_motd_linkage_symbols": [x for x, _, _ in main_symbols
                                        if any(y in x.lower() for y in ("screenmultiplayer", "motd", "messageoftheday"))],
        "menu_entry_callback": f"0x{word(0x630d6c):08x}",
        "menu_entry_callback_has_snr_relocation": 0x630d6c in relocation_sites,
        "main_imports_extractor_module_loader": [x for x, _, kind in main_symbols
                                                if kind == 1 and x.startswith("LoadModule__14PatchExtractor")],
        "direct_module_manager_calls": calls[0x430fd8],
        "direct_snr_loader_calls": calls[0x2738e0],
        "configuration_commands": commands,
        "disc_modules": modules,
        "scope": "Static observations only; application order requires the accompanying Ghidra trace. Hardware installation is untested.",
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("elf", type=Path)
    parser.add_argument("hog", type=Path)
    args = parser.parse_args()
    try:
        result = inspect(args.elf, args.hog)
    except (OSError, ValueError, UnicodeError, struct.error) as error:
        parser.error(str(error))
    print(json.dumps(result, indent=2))


if __name__ == "__main__":
    main()
