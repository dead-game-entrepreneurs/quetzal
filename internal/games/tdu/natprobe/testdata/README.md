# Native public-URL reply fixture

`native-reply.json` uses synthetic endpoints and CID4001; it contains no login,
ticket or authentication capture. The retail US SLUS_214.90 executable SHA-256 is
`620ea714b1dfd2d003a9791e5fabc2e8c9f1f6ac5c908f5873cd7e54972c7bf5`.

The fixture executes raw UDP demultiplexer0x244750, engine entry0x21cf30,
public-URL parser0x21bb60, native raw readers0x236320/0x236230 and the real
JobGetPublicURL callback0x21f7c0. Both accepted adapter header byte orders reach
the completion store; a PRUDP USER prefix presented to this raw demux does not.
The raw response is `cd80000002` followed by an ASCII StationURL and NUL.

The offline MIPS64 harness supplies allocation, buffer storage, StationURL
property formatting/copying, service/job context lookups, mutexes and cleanup.
Header comparisons, opcode parsing, raw reads, CID selection, callback dispatch
and the completion flag store execute retail instructions. It normalizes EE
128-bit register saves, low64 PCPYLD moves and the checked32-bit byte-count
multiply. This verifies parsing/lifecycle, not an entire console session.

Regenerate from the sibling private research checkout:

```sh
python3 tools/verify_nat_probe.py --server-root ../quetzal > work/gameplay/native-nat-reply.json
```

The tool reads only the user's previously extracted verified executable through
`tools/ee_validation.py`. It does not read authentication captures. Compare the
output with this fixture. See `docs/games/tdu/nat-probe.md` for the asymmetric
PRUDP USER request and raw UDP response framing.
