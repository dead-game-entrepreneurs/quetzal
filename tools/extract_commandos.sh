#!/usr/bin/env bash
# Extract the US PS2 online images and configuration without changing the disc.
set -euo pipefail
cd "$(dirname "$0")/.."
iso=${1:?Usage: tools/extract_commandos.sh /path/to/Commandos-Strike-Force-USA.iso}
mkdir -p disc/commandos work/commandos
7z x -y -odisc/commandos "$iso" SLUS_211.03 SYSTEM.CNF COMMX_FR.ELF \
  FINAL.BIN FIN_RED.BIN RVOUS_VF.BIN CYBOR_VF.BIN 'CONFIG/*' IRX/QNET.IRX
sha256sum disc/commandos/SLUS_211.03 disc/commandos/COMMX_FR.ELF \
  disc/commandos/FINAL.BIN disc/commandos/FIN_RED.BIN \
  disc/commandos/RVOUS_VF.BIN disc/commandos/CYBOR_VF.BIN \
  disc/commandos/CONFIG/JUEGOVER.CFG > work/commandos/executable.sha256
readelf -h -l -S disc/commandos/COMMX_FR.ELF > work/commandos/elf.txt
python3 tools/elf_strings.py disc/commandos/COMMX_FR.ELF > work/commandos/bootstrap.strings.tsv
