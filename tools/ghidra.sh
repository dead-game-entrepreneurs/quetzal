#!/usr/bin/env bash
# Keep Ghidra's settings/cache in this workspace as well as its project.
set -euo pipefail
cd "$(dirname "$0")/.."
ghidra_install=${GHIDRA_INSTALL_DIR:-$HOME/.local/opt/ghidra_12.1.4_PUBLIC}
export XDG_CONFIG_HOME="$PWD/work/ghidra-config"
export XDG_CACHE_HOME="$PWD/work/ghidra-cache"
export GHIDRA_HEADLESS_JAVA_OPTIONS="${GHIDRA_HEADLESS_JAVA_OPTIONS:-} -Duser.home=$PWD/work/ghidra-home"
mkdir -p ghidra work/25tolife
case "${1:-}" in
  import-commandos)
    shift
    mkdir -p work/commandos
    exec "$ghidra_install/support/analyzeHeadless" ghidra commandos-online \
      -import disc/commandos/COMMX_FR.ELF -processor r5900:LE:32:default \
      -scriptPath ghidra/scripts -preScript LoadCommandosOverlays.java disc/commandos \
      -analysisTimeoutPerFile 300 -log work/commandos/import-online.log "$@"
    ;;
  import-patcher)
    shift
    python3 tools/snr2.py disc/25tolife/modules/PATCHLIB.REL work/25tolife/PATCHLIB --text-end 0x30300
    exec "$ghidra_install/support/analyzeHeadless" ghidra quetzal \
      -import work/25tolife/PATCHLIB.bin -loader BinaryLoader -loader-baseAddr 0x0 \
      -processor r5900:LE:32:default -scriptPath ghidra/scripts \
      -preScript ApplySnr2Symbols.java work/25tolife/PATCHLIB.syms.tsv 0x30300 \
      -analysisTimeoutPerFile 300 "$@"
    ;;
  import)
    shift
    exec "$ghidra_install/support/analyzeHeadless" ghidra quetzal \
      -import disc/25tolife/SLUS_210.16 -processor r5900:LE:32:default \
      -analysisTimeoutPerFile 600 -log work/25tolife/import.log "$@"
    ;;
  decompile|decompile-patcher|decompile-commandos)
    program=SLUS_210.16
    project=quetzal
    if [ "$1" = decompile-patcher ]; then program=PATCHLIB.bin; fi
    if [ "$1" = decompile-commandos ]; then program=COMMX_FR.ELF; project=commandos-online; fi
    shift
    if [ "$#" -lt 2 ]; then
      echo 'Usage: tools/ghidra.sh decompile[-patcher|-commandos] OUTPUT ADDR|code:ADDR|refs:TEXT|addrrefs:ADDR|callers:ADDR|name:TEXT|range:START:END|asm:ADDR ...' >&2
      exit 2
    fi
    exec "$ghidra_install/support/analyzeHeadless" ghidra "$project" \
      -process "$program" -noanalysis -readOnly -scriptPath ghidra/scripts \
      -postScript Research.java "$@"
    ;;
  *)
    echo 'Usage: tools/ghidra.sh import[-patcher|-commandos] | decompile[-patcher|-commandos] OUTPUT SELECTOR ...' >&2
    exit 2
    ;;
esac
