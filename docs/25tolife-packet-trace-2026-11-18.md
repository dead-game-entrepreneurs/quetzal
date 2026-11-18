# 25 to Life packet trace: host departure and stale sessions

The preserved backend capture confirms that a host departure deletes a gathering
while another client is still using it. The remaining client then executes the
stock game's host-migration URL-clear operation, which the server also rejects.
This identifies an incomplete server migration lifecycle. It does not establish
the cause of reported client crashes or exclude relay packet loss.

## Scope and evidence

- Capture window: November 18, 2026, 19:30:00–21:06:16 UTC
  (13:30:00–15:06:16 America/Chicago).
- Incident server revision: `5eb84bc354b573b5e8d15ddd911167ceb3b04c55`.
- Source traced and locally replayed: `8144cb3538e3a4fcf7b268da4e1b675f555a498b`.
  The intervening changes to the handlers below add diagnostics, without changing
  their deletion, lookup or URL-authorization decisions.
- Retail executable: `SLUS_210.16`, SHA-256
  `52963efa0a12a44db01dbfad65d9bbb865c802c7ddf4513855913314b69cdba3`.
- Analysis reassembled reliable backend DATA, separated connection generations,
  deduplicated retransmissions, and decoded RMC requests and replies. Actor names,
  account details, endpoint addresses and raw payloads are omitted here.
- Raw captures and Ghidra exports remain private. This document contains metadata
  and observations only.

## Observed host-departure sequence

Times below are UTC. Host and survivor refer to distinct authenticated clients.

| Time | Actor | Request | Observed response / code consequence |
| --- | --- | --- | --- |
| 20:21:56.370 | Host | `21/4`, update `TTLSession` 10039 | Successful RPC reply. |
| 20:21:57.180 | Host | `50/1`, end participation in 10039 | Accepted; owner branch deletes the gathering. |
| 20:21:57.601 | Survivor | `21/21`, find gathering 10039 | `false` and empty `AnyData`. |
| 20:21:59.271 | Survivor | `21/27`, update URL of 10039 to the empty string | Application result `false`. |
| 20:22:24.764 | Survivor | `21/4`, empty record type and gathering ID 0 | `GenericFailure` (`0x80010002`). |
| 20:22:41.817 | Survivor | Same invalid update shape | `GenericFailure`. |
| 20:23:18.655 | Survivor | Same invalid update shape | `GenericFailure`. |

The source path is:

1. [`sessionRegistry.extension`](../internal/games/ttl25/sessions_extension.go)
   deletes the owner's gathering on `50/1`, even with remaining participants.
   `finishRound` retains reporting authority only for `TTLRound`, so it does not
   preserve this `TTLSession`.
2. [`sessionRegistry.operation`](../internal/games/ttl25/sessions_operations.go)
   returns `false` plus empty `AnyData` for a missing `21/21` lookup.
3. [`sessionRegistry.lobby`](../internal/games/ttl25/sessions_lobby.go)
   treats `21/26` and `21/27` identically: existing room, original owner connection,
   and a nonempty valid URL are required. The captured operation fails all three
   requirements. Merely retaining the room would not make this request succeed.
4. The later empty-type updates fail schema validation. The capture establishes
   their order after the failed lookup; the exact client assignment that produced
   ID zero has not been traced and is not claimed as proven.

Two further updates referenced gatherings already removed by their
owners: 10043 at 20:42:59 and 10051 at 20:45:44. Those updates returned `false`.

## Stock client migration behavior

Read-only Ghidra inspection shows a `HostMigrationExtension` constructed at
`0x002fddd0`. Its native callback at `0x002fe628` handles event type 9 and sets a
pending flag when the relevant station is local. The tick path `0x002fdd60` calls
`0x002fe8c8`, which iterates tracked gathering IDs and sends an empty URL through
`0x005378b8` → `0x0053a1c0`, the `21/27` writer.

The string source at `0x00678ec8` is empty. Consequently the captured empty URL is
an intentional stock migration operation, rather than an incomplete new-host
endpoint. This proves the client supports a migration path; successor authority
and complete backend ownership-transfer semantics still need tracing.

## NAT timeouts and stale connections

Two server-to-client `3/2` instructions exhausted the observed six transmissions
without an ACK. Both carried direct endpoints, not relay endpoints.

| Instruction time UTC | Last client backend packet | Prior silence | Subsequent activity |
| --- | --- | --- | --- |
| 20:19:04.193 | 20:18:22.953 | 41.24 seconds | New handshake at 20:20:57.488. |
| 20:40:25.013 | 20:38:57.280 | 87.73 seconds | No later received packet in the snapshot. |

[`requestProbes`](../internal/protocol/quazal/nat.go) and the title's
[`probeTargets`](../internal/games/ttl25/connectivity.go) authorize registered
connections and published room membership. They do not require recent inbound
activity. The transport's default idle lifetime is 120 seconds, so a silent
connection can remain eligible for an introduction.

The reliable transport retries every 500 ms, up to five retries after the initial
send. [`Server.expire`](../internal/protocol/prudp/server.go) then drops an exhausted
connection; `drop` calls the service disconnect hook, which removes session
membership and owned rooms. The code predicts this cleanup, but the old console
logs are unavailable to verify the precise drop timestamp in this incident.

Silence preceded both instructions. The trace does not establish whether the
client quit, crashed, stalled or lost network connectivity.

## Relay evidence and limits

The capture contains 17 connection lookups returning two candidates each, including
one relay candidate, and three server introductions containing relay endpoints
(20:18:27.986, 20:21:30.888, 20:31:24.723 UTC). This shows traversal activity, not
successful bidirectional gameplay forwarding.

There were no observed backend checksum, LZO, framing-bound or RMC-envelope decode
failures. Of 1,303 distinct reliable server frames, 48 were retransmitted, with 75
extra sends. These are backend measurements, not native relay loss measurements.

Native gameplay datagrams were not recorded by the backend capture. The old
container's console logs were not preserved before its replacement. As a result,
mapping proofs, forwarding totals, expiry decisions and packet drops for that run
cannot be reconstructed. In particular, current relay code silently drops some
packets (including oversized/rate-limited traffic) and ignores UDP send errors;
its forwarding counters count send attempts, not confirmed delivery.

## Local reproduction and repair boundary

A temporary Go diagnostic using synthetic accounts and an in-memory gathering
reproduced deletion with a surviving participant, the empty lookup reply, rejection
of the empty migration URL both before and after deletion, and rejection of an
empty-type/zero-ID update. It passed against revision `8144cb3` in the existing
Go 1.27.4 Docker toolchain. The diagnostic was removed afterward; no runtime code
was changed or deployed by this investigation.

A complete repair needs a bounded migration lifecycle with verified successor
authority, endpoint invalidation and safe relay/membership cleanup. Accepting
zero-ID updates, allowing arbitrary members to take ownership, or keeping deleted
rooms indefinitely would not establish correct migration behavior. Stock console
compatibility remains required; this investigation used no client patches.

The subsequent server repair is documented in
[host migration and stale sessions](games/25tolife/host-migration.md). The trace
above describes the pre-repair incident and the diagnostic reproduction, not
the current implementation.
