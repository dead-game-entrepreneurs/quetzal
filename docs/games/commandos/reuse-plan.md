# Commandos reuse assessment and implementation plan

Based on the US PS2 static sweep linked from [online-map.md](online-map.md).
The [experimental adapter](running.md) now implements the initial account/lobby
surface. This research plan records reuse boundaries and retail acceptance
criteria; implementation alone does not establish stock-client compatibility.

## Reuse matrix

| Existing code | Assessment | Necessary Commandos work |
| --- | --- | --- |
| `internal/game`, `internal/app`, tenant locks/state/captures | Reuse | New explicit `commandos` definition and isolated state directory |
| `internal/protocol/prudp/packet.go` | Framing/checksum match static reader/writer | Access-key seed `0xa6`; verify effective payload codec in startup capture |
| PRUDP reliability/windowing/fragment handling | Reuse as infrastructure | Retail timing, fragmentation and disconnect testing |
| `internal/protocol/quazal/rmc.go` | Same protocol/request/method scalar conventions | End-to-end login/RMC framing verification with stock client |
| `internal/quazalcrypto` | Password iterations and RC4/keyed-container/ticket structure match | Synthetic native checks for auth vectors and title principal policy |
| Shared Quazal login/ticket/registration service | Strong reuse candidate | Configurable bootstrap identity/key, name-length handling and signup parameter 0 |
| Shared account storage | Reuse player persistence and initial-hash storage | Password-change operation, result policy and ticket/session invalidation |
| TTL gathering lifecycle and authority design | Adapt | New Commandos codec, regex search, initial-page sentinel, count/host policy |
| TTL concrete session/round codec | Incompatible fields; shared length framing | Commandos-specific field layout inside the common Data envelope |
| TTL friends/clans/ranking/competition protocol 101/26 | No established Commandos use | Keep out of the initial adapter |
| Shared NAT machinery | Adapt | Verify Commandos URL scheme, peer direction and identity checks |
| Shared native relay, extracted from TTL | Reused with title-specific browse/introduction policy | Blocked-direct fallback and same-IP LAN relay joins passed; Internet NAT, full rounds and migration remain open |
| TDU native master/proxy/edNet/resources/presence/races | No corresponding client path found | Do not include in Commandos startup |
| TTL plain payload wrapper | Matches stock US auth and secure startup, tested 2026-11-23 | Default plain; account creation and named login passed on both LAN PCs |
| TDU LZO gameplay assumption | Incompatible with identified Net-Z root codec | Commandos installs LZSS and a different RC4 key for native gameplay |

The title's `0x457710` installs LZSS object `0x6def90` and RC4 object `0x6860f0`
on the Net-Z root transport using `0x676750`/`0x676760`. The RC4 key comes from
`0x7571c0`, with explicit length **53**, including its trailing NUL.
The LZSS routines are `0x6df5e0`/`0x6dfa30`: a 4096-byte history, initial
spaces, flag bytes and 12-bit offset/4-bit length tokens. These gameplay
transforms are not the password-derived ticket keys. Installing an identical
RC4 class somewhere in the binary does not establish `CD&ML` as the effective
gameplay or backend packet key.

## Build order and acceptance criteria

1. **Transport and account startup.** Capture a stock startup attempt while
   resolving the configured authentication hostname locally. Compare SYN,
   CONNECT and first RMC DATA with the static framing/checksum map. Establish
   whether stream 3 is raw or transformed before adding a payload mode. Parameterize
   bootstrap credentials without changing TTL/TDU defaults. Verify normal login,
   wrong password, duplicate signup/recovery and password change in synthetic
   tests, then in the retail UI. Keep DNAS separate.
2. **Lobby codecs and discovery.** Implement `Commandos3DGameGathering`, bounded
   Any/list readers, retained fields and supported regex patterns. Add 21/19
   with the observed range sentinel/count behavior. Test synthetic host/browse
   and malformed/truncated records against the native trace; then verify that
   a second stock client sees the room, name, map, mode and player count.
3. **Host/join/leave and peer admission.** Implement the retained 21/50 operations,
   bind membership/URLs to authenticated connections, and correlate NAT probe
   responses. Verify two-client direct joining, ready/start, movement/shooting,
   round transition and clean departure before claiming multiplayer support.
4. **Migration and wider peer features.** Recover host-election authority and
   URL updates; verify chat, voice/mute, private/password behavior, latency
   probes and adverse-NAT cases. Adapt relay support only after its native
   handshake identity and endpoint ownership are established.
5. **Only evidence-driven extra backend services.** Add friends, persistent
   statistics, rankings, clans or downloads if a real PS2 request/caller proves
   a requirement. The present sweep does not support importing those TTL/TDU
   services simply because shared assets mention them.

## Reproducing the research

```sh
tools/extract_commandos.sh '/path/to/Commandos - Strike Force (USA) (En,Fr,Es).iso'
tools/ghidra.sh import-commandos
tools/ghidra.sh decompile-commandos work/commandos/login.c \
  0x461c80 0x465840 0x8c28e0 0x8c2ff0 0x8ca930
tools/ghidra.sh decompile-commandos work/commandos/matchmaking.c \
  range:0x45fb40:0x45ff00 range:0x8d26c0:0x8d3880 0x456080
python3 tools/verify_commandos_gathering.py
```

The verifier needs local Python Unicorn. It checks known executable hashes,
translates only the needed low-word EE saves/loads/pointer arithmetic for
MIPS32, hooks stream primitives and records every translated instruction that
actually executes. It also executes the retail checksum routine against 292
synthetic length/seed cases; all agree with the shared checksum arithmetic.
Outputs go to ignored `work/commandos/`.

`import-commandos` preserves original disc bytes in their mapped regions and
records overlay hashes in comments/logs; it adds only analysis annotations and
zeroed runtime BSS. `-overwrite` deliberately replaces the local analysis
program and its annotations. Query commands are read-only; `code:ADDRESS`
discovers missing functions only within the temporary query session. `range:`
uses an exclusive end address; `asm:` exports instructions for a known function.
