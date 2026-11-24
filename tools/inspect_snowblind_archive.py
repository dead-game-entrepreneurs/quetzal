#!/usr/bin/env python3
"""Extract selected US Snowblind research resources into an ignored directory.

This reads the RKV index and its relative-distance LZSS stream. It does not
modify the archive or supply any assets to the server. The index layout is
inferred from the supported retail archive; decoded goefile sizes are checked.
"""

import argparse
import json
import struct
from pathlib import Path


RESOURCES = ("Code", "LoaderCode", "MPCode", "NetUI", "PreGame", "game_gfl")
MAX_RESOURCE_SIZE = 32 * 1024 * 1024


def read_index(stream):
    header = stream.read(8)
    version, index_size = struct.unpack("<II", header)
    if version != 1 or not 8 <= index_size <= 16 * 1024 * 1024:
        raise ValueError("unsupported RKV index")
    stream.seek(0)
    data = stream.read(index_size)
    pos = 8

    def u32():
        nonlocal pos
        if pos + 4 > len(data):
            raise ValueError("truncated index")
        value = struct.unpack_from("<I", data, pos)[0]
        pos += 4
        return value

    def text():
        nonlocal pos
        size = u32()
        if size > 4096 or pos + size > len(data):
            raise ValueError("invalid index string")
        value = data[pos : pos + size].decode("ascii")
        pos += size
        return value

    def names():
        count = u32()
        if count > 64:
            raise ValueError("invalid index name count")
        return [text() for _ in range(count)]

    languages, types = names(), names()
    count = u32()
    if count > 100000:
        raise ValueError("invalid entry count")
    entries = {}
    for _ in range(count):
        kind, name, variants = u32(), text(), u32()
        if kind >= len(types) or not 1 <= variants <= 64:
            raise ValueError("invalid resource entry")
        records = [(u32(), u32()) for _ in range(variants)]
        if name in entries:
            raise ValueError("duplicate resource name")
        entries[name] = {"type": types[kind], "records": records}
    return (index_size + 2047) & ~2047, languages, entries


def decode(data):
    out = bytearray()
    pos = 0
    while pos < len(data):
        flags = data[pos]
        pos += 1
        for bit in range(8):
            if pos == len(data):
                break
            if flags & (1 << bit):
                out.append(data[pos])
                pos += 1
            else:
                if pos + 2 > len(data):
                    raise ValueError("truncated LZSS reference")
                low, high = data[pos : pos + 2]
                pos += 2
                distance = low | ((high & 0xF0) << 4)
                count = (high & 0x0F) + 3
                if not 0 < distance <= len(out):
                    raise ValueError("invalid LZSS distance")
                for _ in range(count):
                    out.append(out[-distance])
            if len(out) > MAX_RESOURCE_SIZE:
                raise ValueError("decoded resource exceeds limit")
    if len(out) < 12 or out[:8] != b"goefile\0":
        raise ValueError("resource is not a goefile")
    if struct.unpack_from("<I", out, 8)[0] != len(out):
        raise ValueError("decoded goefile length mismatch")
    return out


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("archive", type=Path)
    parser.add_argument("--resource", action="append", choices=RESOURCES)
    parser.add_argument("--out", type=Path, default=Path("work/snowblind/resources"))
    args = parser.parse_args()
    args.out.mkdir(parents=True, exist_ok=True)
    manifest = {}
    with args.archive.open("rb") as stream:
        base, languages, entries = read_index(stream)
        for name in args.resource or RESOURCES:
            entry = entries[name]
            if entry["type"] != "null" or len(entry["records"]) != 1:
                raise ValueError("unsupported resource variant")
            sector, size = entry["records"][0]
            if size > MAX_RESOURCE_SIZE:
                raise ValueError("stored resource exceeds limit")
            stream.seek(base + sector * 2048)
            packed = stream.read(size)
            if len(packed) != size:
                raise ValueError("truncated resource")
            decoded = decode(packed)
            (args.out / (name + ".goe")).write_bytes(decoded)
            manifest[name] = {"stored": size, "decoded": len(decoded)}
    (args.out / "manifest.json").write_text(
        json.dumps({"languages": languages, "resources": manifest}, indent=2) + "\n"
    )
    print(json.dumps(manifest, indent=2))


if __name__ == "__main__":
    main()
