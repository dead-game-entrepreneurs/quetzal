#!/usr/bin/env bash
# Extract the US PS2 online executable and startup configuration for analysis.
set -euo pipefail
cd "$(dirname "$0")/.."
iso=${1:?Usage: tools/extract_snowblind.sh /path/to/Project-Snowblind-USA.iso}
mkdir -p disc/snowblind work/snowblind
7z x -y -odisc/snowblind "$iso" SLUS_210.37 BO3GM.ELF BO3NETGM.ELF \
  DNAS.REL SYSTEM.CNF FUN/BO3/GAME.INI MODULES/QNET.IRX
sha256sum disc/snowblind/SLUS_210.37 disc/snowblind/BO3GM.ELF \
  disc/snowblind/BO3NETGM.ELF disc/snowblind/DNAS.REL \
  > work/snowblind/executable.sha256
readelf -h -l -S disc/snowblind/BO3NETGM.ELF > work/snowblind/elf.txt
python3 tools/elf_strings.py disc/snowblind/BO3NETGM.ELF > work/snowblind/online.strings.tsv
python3 tools/elf_strings.py disc/snowblind/SLUS_210.37 > work/snowblind/bootstrap.strings.tsv
