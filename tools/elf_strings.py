#!/usr/bin/env python3
"""List printable strings in file-backed ELF32 load segments, with runtime addresses."""
import argparse
import re
import struct
from pathlib import Path


def strings(path, minimum):
    data = path.read_bytes()
    if data[:6] != b"\x7fELF\x01\x01":
        raise ValueError("expected a little-endian ELF32 executable")
    header = struct.unpack_from("<16sHHIIIIIHHHHHH", data)
    phoff, phsize, phcount = header[5], header[9], header[10]
    segments = []
    for i in range(phcount):
        kind, offset, vaddr, _, size, _, _, _ = struct.unpack_from(
            "<IIIIIIII", data, phoff + i * phsize
        )
        if kind == 1:
            segments.append((offset, offset + size, vaddr))
    pattern = rb"[\x20-\x7e]{%d,}" % minimum
    for match in re.finditer(pattern, data):
        for start, end, base in segments:
            if start <= match.start() and match.end() <= end:
                yield match.start(), base + match.start() - start, match[0].decode("ascii")
                break


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("elf", type=Path)
    parser.add_argument("--minimum", type=int, default=5)
    args = parser.parse_args()
    if args.minimum < 1:
        parser.error("--minimum must be positive")
    print("file_offset\tvirtual_address\ttext")
    for offset, address, value in strings(args.elf, args.minimum):
        print(f"0x{offset:08x}\t0x{address:08x}\t{value}")


if __name__ == "__main__":
    main()
