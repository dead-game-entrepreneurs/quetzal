#!/usr/bin/env python3
"""List or extract entries from 25 to Life's unencrypted PS2 MAIN.HOG."""
import argparse
import fnmatch
import hashlib
import json
import struct
from pathlib import Path


def entries(stream):
    stream.seek(0, 2)
    archive_size = stream.tell()
    stream.seek(0)
    header = stream.read(24)
    version, kind, table, flags, _, count, table_size = struct.unpack("<HHIIIII", header)
    if (version, kind) != (1, 2) or table + count * 16 > archive_size:
        raise ValueError("unsupported or invalid HOG header")
    if flags != 0:
        raise ValueError("encrypted HOG tables are not supported (use MAIN.HOG)")
    names_start = table + count * 16
    names_end = table + table_size
    if not names_start <= names_end <= archive_size:
        raise ValueError("invalid HOG name table")
    for index in range(count):
        stream.seek(table + index * 16)
        name_offset, offset, size, name_hash = struct.unpack("<IIII", stream.read(16))
        if not names_start <= name_offset < names_end or offset + size > archive_size:
            raise ValueError(f"invalid HOG entry {index}")
        stream.seek(name_offset)
        raw = stream.read(min(1024, names_end - name_offset))
        if b"\0" not in raw:
            raise ValueError(f"unterminated HOG name {index}")
        name = raw.split(b"\0", 1)[0].decode("ascii")
        yield name, offset, size, name_hash


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("archive", type=Path)
    parser.add_argument("patterns", nargs="*", help="case-insensitive shell patterns over archive names")
    parser.add_argument("--output", type=Path, help="extract matching entries here, using basenames")
    args = parser.parse_args()
    if args.output and not args.patterns:
        parser.error("extraction requires at least one explicit pattern")
    manifest = []
    names = set()
    with args.archive.open("rb") as stream:
        for name, offset, size, name_hash in entries(stream):
            if args.patterns and not any(fnmatch.fnmatchcase(name.upper(), p.upper()) for p in args.patterns):
                continue
            record = {"name": name, "offset": offset, "size": size, "name_hash": f"0x{name_hash:08x}"}
            if args.output:
                basename = name.replace("\\", "/").split("/")[-1]
                if basename in ("", ".", "..") or basename in names:
                    raise ValueError(f"unsafe or duplicate output name: {name}")
                names.add(basename)
                stream.seek(offset)
                data = stream.read(size)
                args.output.mkdir(parents=True, exist_ok=True)
                (args.output / basename).write_bytes(data)
                record["sha256"] = hashlib.sha256(data).hexdigest()
            manifest.append(record)
    if args.patterns and not manifest:
        parser.error("no matching entries")
    print(json.dumps(manifest, indent=2))


if __name__ == "__main__":
    main()
