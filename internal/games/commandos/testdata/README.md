# Commandos synthetic native gathering fixture

`gathering-native.json` contains synthetic field values traced through the US
retail writer `0x45fb40` and reader `0x45fc00`, with stream primitives hooked.
It contains no executable bytes, game assets or player data. SDK String framing
is applied to the traced String value when producing `field_body_hex`.

Run `python3 tools/verify_commandos_gathering.py` with the supported extracted
images. Its ignored output `work/commandos/gathering-native.json` includes the
field body, 559 primitive calls, executed EE instruction translations and 292
checksum checks. The checked-in fixture retains the synthetic body and build
hashes. This verifies field order/widths, not complete native transport execution.

The fixture is a **field body**, not the complete Any payload. The stock host
capture on 2026-11-23 established that its Data length must be added before
placing it in the outer Any buffer. The codec test wraps the fixture accordingly;
a separate synthetic regression checks the observed outer 600 / inner 596-byte
initial-registration layout. No private host capture is included here.
