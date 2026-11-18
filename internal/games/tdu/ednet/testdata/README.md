`native-resources.json` contains synthetic, copyright-free resource records.
The Open/download (`15bfd0`), Open/upload (`15be00`) and Get (`15bb20`)
request bytes were captured from actual US retail executable instructions.
For configuration Open requests, the fixture also executes option setter
`15c190(this, 1, 2)` and user setter `1569d0(this, 0)`, matching callers
`461ed0`, `4614b0` and `461c80`. These override the constructor's initial
facility 1 with **facility 2**. `configurationRequests` records the exact bodies
for `netconfig.xml`, `p2pkey` and `prize.xml`; the server regression downloads
all three resources through completion. The prior fixture supplied facility 1
directly and therefore missed the retail caller's override.

The response records were supplied to the actual resource-job consumer
`15b5d0`; the fixture records its resulting state, cursor, retry parameters,
resume timer and copied bytes. Real edNet signature/scalar/string/buffer
primitives execute in both directions. The high-byte values check byte order
and full-width fields; the binary chunk checks embedded zeroes and counted
buffer lengths. Error 2 requests a delayed new Open; error 1 ends the job.

Reproduce with your own hash-verified executable and Unicorn installed:

```sh
python3 internal/games/tdu/ednet/testdata/verify_resources.py \
  /path/to/tdu-server/tools > /tmp/native-resources.json
cmp /tmp/native-resources.json internal/games/tdu/ednet/testdata/native-resources.json
```

The verifier reuses the neighboring presence MIPS64 harness and its explicit
EE stack-save instruction normalization. Resource lifecycle/reset, clock,
registration, diagnostics, memory/string intrinsics and reliable-submit
boundaries are supplied by the harness. No resource field writer or reader
is mocked. It executes individual jobs, not a complete retail client or live
network session. Reply contents/retries are replacement server policy, not
recovered original server data. No game executable or resource asset is
included in these fixtures.
