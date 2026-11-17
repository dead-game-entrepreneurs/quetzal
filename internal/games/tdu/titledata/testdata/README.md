# Sanitized retail record fixtures

`native-records.json` contains synthetic records serialized by US PS2 retail
routines `0x2416b0` (EventStats) and `0x24a9d0` (GameStats). The main ELF hash is
recorded in `docs/games/tdu/build.json`; no game instructions or captured user
records are included.

The private verifier runs the actual field traversal in Unicorn MIPS64.
Only 128-bit stack saves/restores are reduced to 64-bit LD/SD, at the addresses
listed in each fixture. Ordinary scalar/string stream I/O is intercepted using
the established little-endian SDK format; all 64-bit field bytes are preserved.
The routine order/offset/width selection executes from the user's executable.
This does not claim native execution of the entire RMC/network stack or retail
online play. The private source/report stay under the sibling research workspace
`work/ps2/verify-title-records.py` and `title-record-native-execution.json`.
