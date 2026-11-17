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
  decompile|decompile-patcher)
    program=SLUS_210.16
    if [ "$1" = decompile-patcher ]; then program=PATCHLIB.bin; fi
    shift
    if [ "$#" -lt 2 ]; then
      echo 'Usage: tools/ghidra.sh decompile OUTPUT ADDR|refs:TEXT|addrrefs:ADDR|callers:ADDR|name:TEXT ...' >&2
      exit 2
    fi
    exec "$ghidra_install/support/analyzeHeadless" ghidra quetzal \
      -process "$program" -noanalysis -readOnly -scriptPath ghidra/scripts \
      -postScript Research.java "$@"
    ;;
  *)
    echo 'Usage: tools/ghidra.sh import | import-patcher | decompile[-patcher] OUTPUT SELECTOR ...' >&2
    exit 2
    ;;
esac
