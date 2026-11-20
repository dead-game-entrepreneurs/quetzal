# World Championship Cards — PS2 protocol notes

Quetzal's `wcc` adapter targets the unmodified US PS2 release. These notes
combine static analysis of the retail executable, synthetic UDP tests and
PCSX2 sessions with up to three stock clients on one LAN (2026-11-18/19); the
in-game results sections say what was confirmed. Addresses are runtime
addresses in `SLUS_213.40`.

## Target build

| Item | Value |
| --- | --- |
| Disc volume | `WC_CARDS` |
| Boot executable | `SLUS_213.40` (`ELFNAME.TXT`: `poker`) |
| SYSTEM.CNF version / video | `1.05` / `NTSC` |
| ELF size | 6,525,788 bytes |
| ELF SHA-256 | `0ced0a6ce6ec552babfd52c8d9bec72a23e286bac3c712be15a73d55bd8bf22d` |
| Entry point | `0x002a9930` |
| Load mapping | file offset `0x1000` → runtime `0x00100000` (single PT_LOAD) |
| Ghidra language | `r5900:LE:32:default` |

Developer Point of View, publisher Crave. Build paths show NetZ/Rendez-Vous
under `C:/projects/Cards/Packages/Quazal/NetZ/PS2/`. The disc ships DNAS
images (`IRX/DNAS300.IMG`, `IRX/DNAS30E.IMG`); DNAS stays a separate service.

## Endpoint and transport

| Item | Value | Evidence |
| --- | --- | --- |
| Host | `wcclive.quazal.net` | Stored by backend settings constructor `0x0028ee38` |
| Auth port | UDP 30280 (`0x7648`) | Same constructor |
| Secure port | UDP 30281 (server choice) | Advertised in the 10/1 reply; not fixed by the client |
| Access key | `uh85sn` (`0x006e3818`) | `0x00291518` → `0x003d13e0` → strncpy to `0x008492c0` (limit `0x7f`) |
| Checksum seed | **0x2b** | Stream setup `0x003d67e8` passes the key to `0x003837a0`, which byte-sums it |
| Payload wrapper | Plain raw/LZO (selector byte) | See below |

Stream setup has a formatted variant (`%s%d`, `0x00701438`) used only when
`DAT_006573c4` is nonzero. It starts at zero and its setter `0x003d1410` has no
callers, so the plain sum applies.

Core construction `0x00355008` installs the compressor when `DAT_00656c40` is
set (initial value 1) and the `CD&ML` RC4 object (`0x003649f8`, key built by
`0x00364ae0`) when `DAT_00656c48` is set. Only `0x00355388` sets that flag, and
nothing calls it, points to it or builds its address. WCC therefore uses
25 to Life's plain wrapper, not TDU's transport RC4. Ticket crypto is unchanged.

No patch or update hosts were found.

## Accounts and login

The shared Quazal implementation fits this build:

- 10/1 reply parse `0x003dd0b8` reads result, PID, ticket buffer, connection
  data (URL, special-protocol list, special URL) and a server-name string.
- Account creation `0x003c7de8` logs in as the SDK guest (`guest` /
  `h7fyctiuucf`), calls 25/1 with String name, String 32-hex initial hash,
  u32 `0x20` and String email, then logs out. The email is always the
  placeholder `qwerty@keyboard.com` (`0x006e3860`).
- The game derives each account password itself. `0x00291518` uppercases the
  username, hashes it from seed `0xb43ac39d`, mixes in four 4-bit fields of a
  third argument and maps the result through `A–Za–z0–9_#` into an 8-character
  password at `this+0x1c0`. The source of that argument is not yet traced; if it
  is per-console, an account may be tied to one memory card or console.
- 25/17 (status String) is called from `0x00291884`.
- Player names may contain spaces between characters but not at either end
  (name-entry error text in `DATA/SHELL.WAD`). The WCC account store uses
  `quazal.NamePolicy{InnerSpaces: true}`; other titles keep the original
  no-space rule. Length stays 3–15, matching the 16-byte name buffer.
  Confirmed in game: an account with a space signed up and logged in.

Secure registration returns `prudp:/` StationURLs, matching what the client
registers (`prudp:/address=…;port=9103;sid=15` from PCSX2), confirmed in game.

## In-game results (PCSX2, 2026-11-18)

Two PCs on one LAN: guest signup (10/1, 10/3, 11/1, 25/1), named login
(+25/17), Health 18/1 every 5 seconds, hosting (21/1, 21/21, 21/11, 21/26,
21/4) and browsing (21/17, 21/21) all completed.

Joining exposed two server issues, now fixed:

- The joiner calls **11/3 (CID 0, host PID) before 21/11**. Lookups and NAT
  probes are therefore allowed toward the host of any listed gathering, not
  only between existing participants.
- The host calls **21/26 LaunchSession with an empty URL**. That now succeeds;
  peers use the host's registered StationURL.

With those fixes, PC1 joined PC2's table, played a full game of War and
finished it; Quick Join and in-table chat also worked. No `ChatGathering`
(chat-lobby room) was registered during testing. Hosting on the other PC failed
in the same session after 11/3 returned the correct host URL: the joiner
waited 15–30 seconds without calling 21/11: its direct connection to the host
got no answer. The retail joiner never requests a NAT probe (3/1). The server
now asks the host to probe the joiner (3/2 InitiateProbe, client dispatcher
`0x00336508`) whenever a joiner resolves it through 11/3, so the host's own
outbound packets open its firewall/NAT to that joiner. Rate-limited to one
per host/joiner pair every 2 seconds. With it, hosting worked from both PCs.

When the host leaves an open table it hands it to the remaining player
(21/36) and leaves (21/12), but the successor sends no 21/4. The table stayed
listed with the departed host's `numPlayers` (2 of 2), so the former host's
game hid it as full. The server now resets `numPlayers` to the live
participant count whenever someone leaves, disconnects or ownership moves.

After that fix the former host could rejoin, but the game could not be
started: the new owner never acted as host (no 21/4 after the rejoin). The
title learns it owns a table from a notification event, not from 21/36. Its
handler `0x0028e7b8` splits `uiType` into `type / 1000` and `type % 1000`:

| Category | Subtypes | Action |
| --- | --- | --- |
| 3 (participation) | 1; 7, 8 | Participant handling; 7/8 pass `uiParam2` to a title callback |
| 4 (ownership changed) | any | `0x002950d8(uiParam2)`: if it is the local PID, take over as host |

The protocol 14 dispatcher `0x003d95e0` decodes 14/1 with `0x003db570`
(u32 `pidSource`, `uiType`, `uiParam1`, `uiParam2`, String `strParam`) and
replies empty. After a successful 21/36 the server now sends every remaining
participant except the caller `14/1 {caller, 4000, gid, new owner, ""}`.
Participation (3xxx) events are not sent; joins worked without them.

With three players (2026-11-19) the third could not join. After resolving
the host (11/3), a newcomer also sends **3/1 RequestProbeInitiation** with the
StationURL of each player already seated, to whom it must connect as well.
Lookups and probes were limited to the host, so the seated player was never
asked to probe and the newcomer timed out. They are now allowed toward anyone
who owns or plays at a listed gathering; players at no table stay private.

Retest with three PCs: all three joined (the newcomer's 3/1 now reached the
seated player), chatted and played. The host then left mid-game. The server
moved the table (21/36), sent both remaining players the 14/1 ownership event,
and the new owner immediately sent 21/27 UpdateSessionURL, so it took over as
host; in-game chat carried on. At the end of the game the table was handed on
and closed normally. No calls were rejected. The session used 21/16 (polled during
play), 21/4, 21/30, 21/11 and 21/12 with no rejected or unsupported calls.
My Statistics updated without any server call, so statistics appear to live
on the memory card.

At the end of a game the host hands the table to a remaining player (21/36
with that player's PID), then leaves (21/12), and that player leaves too. The
first build kept that empty table listed under its new owner; a gathering now
closes when its last participant leaves.

## Protocols

Protocol IDs come from the shared constructor `0x00360b90(protocol, id)`.
Request stubs call `0x00363638(message, protocol, 1)` and then
`0x00360f38(message, method)`. Method names come from the embedded DDL
namespace (`work/wcc/tools/ddl_scan.py`).

| ID | Protocol | Constructed at | Notes |
| --- | --- | --- | --- |
| 3 | NAT traversal | `0x003369f0` | Shared 3/1 probe handling |
| 10 | Authentication | `0x003d9b50` | Shared |
| 11 | Secure connection | `0x003da4f8` | Shared 11/1, 11/3 |
| 14 | Notification | `0x003d1d68` | Server sends 14/1 ownership change (4000) after 21/36; other events not sent |
| 18 / 19 | Health / Monitoring | `0x003f2680` | Title calls 18/1 from `0x0028f3e0`; replies are one bool |
| 21 / 50 | MatchMaking / Ext | `0x003f4738` | 36 and 5 methods |
| 25 | Account management | `0x003c7818` | 20 stubs; 1 and 17 used |

Matchmaking methods reached from game code (direct calls, up to three levels):

| Method | Name | Server |
| --- | --- | --- |
| 21/1 | RegisterGathering | ✅ |
| 21/2 | UnregisterGathering | ✅ owner only |
| 21/4 | UpdateGathering | ✅ owner only |
| 21/11, 21/12 | Participate, CancelParticipation | ✅ |
| 21/16 | GetParticipantsURLs | ✅ session URL, then participants |
| 21/17 | FindByType (String, ResultRange) | ✅ page capped at 100 |
| 21/21 | FindBySingleID | ✅ |
| 21/26 | LaunchSession | ✅ owner only; empty URL accepted |
| 21/29, 21/30 | GetState, SetState | ✅ set is owner only |
| 21/36 | MigrateGatheringOwnership (u32, list<u32>) | ✅ see below |

Also served: 21/13, 15, 20, 22, 27, 28 and 50/1–4. 50/5
`GetGatheringRelations` is declared but unmapped. Other methods fail and log
`wcc unsupported rpc` with protocol and method, which is how field tests should
find gaps. Indirect (virtual) calls are not covered by this call-site survey.

## Gatherings

The DDL declares three classes on top of the SDK `Gathering` base:

| Class | Title fields |
| --- | --- |
| `WCCGathering`, `WackyGathering` | `gameRules` int8, `eyeToyOnly` bool, `pad` int8, `timeout` int8, `isPrivate` bool, `rating` int8, `numPlayers` int16, `isTournament` bool, `zStarted` bool, `strPassword` char[16]: a 26-byte tail, `numPlayers` at tail offset 6 (confirmed in captures) |
| `ChatGathering` | `descript` char[32], `allowVoice` bool, `isPrivate` bool |

The server checks the base header (body length, participant limits,
description string) and keeps the title fields opaque. It rewrites only the
gathering ID, owner and host PIDs, plus `numPlayers` after departures. Private tables are listed like public ones;
the password appears to be a client-side check (unverified).

Lookups (11/3) and probes (3/1) are allowed between players sharing a
gathering and toward anyone who owns or plays at a listed one.

Ownership belongs to the exact registering connection. If the owner
disconnects with participants left, the gathering leaves public listings for
30 seconds. During that window its participants can still read it and one can
claim it with 21/36. The first listed candidate who is a live participant
becomes owner. Otherwise it closes. Any gathering also closes as soon as its
last participant leaves (21/12 or 50/1). NAT probes (3/1) and 11/3 lookups are
allowed only between players sharing a gathering.

## Open questions

See the [retail test checklist](testing.md). Answered so far: a joiner
resolves the host with 11/3 and connects directly (the server-initiated 3/2
opens the host's side); with three or more players it also sends 3/1 for the
players already seated; ownership changes need a 14/1 event; player names may
contain inner spaces.

1. Does the derived password survive a game restart and a second console?
   Watch for the third argument to `0x00291518` changing between sessions.
2. Participation events (3xxx) are still not sent; nothing has needed them
   so far, including a host leaving mid-game with three players.
3. Chat-lobby rooms are a cut feature. The disc still has the `ONLCHTLB`
   screen (buttons `B_JPCR`, `B_JPubCR`, `B_CCR`, `B_BTML`) and its text ("CHAT
   LOBBY", "We were not able to create a chat room online"), and the code keeps
   its button table at `0x005c5508`, but no screen definition references that
   table or the screen name. The main lobby screen `ONLMLB` has only `B_GL`,
   `B_MS`, `B_MO` and `B_EO` (Game Lobby, My Statistics, My Options, Exit
   Online; table `0x005c54a0`). No session has registered or searched for a
   `ChatGathering`; game code searches by type only for `WCCGathering`
   (`0x00295888`). Game code builds `WCCGathering` objects (vtable
   `0x0069d310` at `0x00291df0` and `0x00292fa0`), but the `ChatGathering`
   vtable `0x0069d138` is used only by its generated factory `0x00282e60`:
   the chat-lobby logic itself was removed. Restoring it would mean writing
   new game code (a disc/executable patch), outside this project's
   no-client-patch rule.
4. Datagram budget: the adapter keeps 25 to Life's 1000-byte cap; large
   FindByType pages fragment. Reliable retransmission uses 25 to Life's retry
   grace (20 × 750 ms, about 15 s, instead of the 2.5 s default) after one WCC
   session ended with "reliable retries exhausted"; watch whether that recurs.
5. Voice (`WCC Headset Channel`), friends, four or more players, game types
   other than War, and play across the Internet.

## Local research workflow

Extracted files stay in ignored paths (`disc/wcc/`, `work/wcc/`, `ghidra/wcc.*`).
Helpers in `work/wcc/tools/`:

- `iso.py`: list or extract ISO9660 files without 7-Zip.
- `ddl_scan.py`, `ddl_dump.py`: parse the embedded DDL namespace.
- `callgraph.py`: find game-code call sites that reach given stubs.
- `decompile.sh OUTPUT SELECTOR…`: read-only `Research.java` export.

The Ghidra project was analysed on Windows (`ghidra/wcc.gpr`). On Linux,
Ghidra's launcher splits `-D` options at spaces, so run it from a path without
spaces (the helpers use a `~/q` symlink).
