#!/usr/bin/env bash
# Extract the executable, networking assets and module archive; keep the ISO intact.
set -euo pipefail
cd "$(dirname "$0")/.."
iso=${1:?Usage: tools/extract_25tolife.sh /path/to/25-to-Life-USA.iso}
mkdir -p disc/25tolife work/25tolife
7z x -y -odisc/25tolife "$iso" SLUS_210.16 SYSTEM.CNF \
  'DATA_PS2/CONF/*' DATA_PS2/MAIN.HOG DATA_PS2/IRX/QNET.IRX
sha256sum disc/25tolife/SLUS_210.16 > work/25tolife/executable.sha256
readelf -h -l -S disc/25tolife/SLUS_210.16 > work/25tolife/elf.txt
python3 tools/elf_strings.py disc/25tolife/SLUS_210.16 > work/25tolife/strings.tsv
python3 tools/hog_extract.py disc/25tolife/DATA_PS2/MAIN.HOG '*PATCH*.REL' \
  --output disc/25tolife/modules > work/25tolife/patch-modules.json
