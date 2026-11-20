# 25 to Life — PS2 protocol notes

Quetzal's first adapter targets the unmodified US PS2 release. The implementation
was researched from the retail executable and checked against two-client game
tests. These notes record the current findings rather than development logs.

## Target build

| Item | Value |
| --- | --- |
| Disc volume | `SLUS_21016` |
| Boot executable | `SLUS_210.16` |
| SYSTEM.CNF version / video | `1.01` / `NTSC` |
| ELF size | 6,567,936 bytes |
| ELF SHA-256 | `52963efa0a12a44db01dbfad65d9bbb865c802c7ddf4513855913314b69cdba3` |
| Ghidra language | `r5900:LE:32:default` |
| Entry point | `0x00100008` |

The ELF load segment maps file offset `0x1000` to runtime `0x00100000`.
`tools/elf_strings.py` reads program headers rather than assuming that offset
for every future title. See [research setup](../../research.md) for local tools.

## Endpoints and update checks

The connection-screen initialization at `0x00187888` replaces a PC hostname
in the ELF's mutable hostname buffer with the PS2 endpoint and writes port
`0x75a8` (30120). Static strings alone therefore misidentify the active host.

| Host | Authentication UDP port | Evidence |
| --- | --- | --- |
| `ttlliveps2.quazal.net` | 30120 | Default initialization `0x00187888` |
| `ttl1.quazal.net` | 10100 | Alternate selector `0x00187978` |
| `ttl2.quazal.net` | 10105 | Same selector |
| `ttl3.quazal.net` | 10110 | Same selector |

The default endpoint is observed in game. The menu exposure of the alternate
selector values is not established. The server advertises secure services on
UDP 30121. Product string `25ToLife|PS2|EFIGS-NTSC` comes from `0x00342b30`;
its presence is not evidence of a numeric title ID.

The stock patch module requests `/TTL/final-version.txt` from
`ttl1.patch.eidos.com`, `ttl2.patch.eidos.com` or `ttl3.patch.eidos.com` on TCP 80.
Patch initialization starts at `0x0018b1b0` and version checking at `0x0018b810`.
The USA path loads `PatchLib.rel`; the PAL module is separate. The stock
HttpMgr maps HTTP 404 to -404 and the version parser clears that result into
its successful no-update state. The tested deployment served an empty 404
through an existing web server. Quetzal provides the same response on its HTTP
listener; deploy a port-80 web server or proxy separately.

The [native-updater trace](native-updater.md) covers the retail memory-card
update path, archive reader, local file preparation and the limits established
so far for using it to fix the disappearing MOTD.

## Transport and authentication

Backend PRUDP uses checksum seed **0xff**. The initial guest request was
protocol 10/1, SDK String `guest`, wrapped as selector 0 plus a length-prefixed
RMC record. A later retail profile request used selector 1 (LZO1X). This title
uses the plain raw/LZO wrapper; applying another game's transport RC4 corrupts
its payloads. Ticket and challenge containers still use their own RC4/HMAC keys.
Native gameplay PRUDP is separate and uses checksum seed **0**.

Auth endpoint construction is at `0x00548340`; secure endpoint construction at
`0x005487f0`; URL registration begins at `0x00549770`. Key derivation uses
PID-based repeated MD5, with `65000 + (PID & 0x3ff)` extension iterations.
The guest setup is at `0x0034e500`. Its embedded SDK guest identity is a protocol
constant, not an operator credential or shared named-player account.

Implemented flow:

1. 10/1 selects guest or named identity and returns password-derived validation.
2. 10/3 issues a session key and authenticated service ticket for the destination.
3. Secure CONNECT validates the destination ticket and nonce proof, rejects
   replay and returns nonce + 1.
4. 11/1 registers StationURLs only after the secure proof succeeds.
5. 25/1 creates an account from a registered guest. Profile methods 25/13 and
   25/17, plus contact update 25/12, complete the observed Eidos signup flow.

Secure authority is tied to the exact proved peer generation. Transport
CONNECT alone is insufficient. Profile/contact records are persisted as entered
by the authenticated player; captures are never used to reconstruct them.
The SDK qResult success value is `0x00010001`, while some title application
statuses use **0** and other methods return a one-byte bool.

## Ranking and round reporting

Login recovers or registers the shared Nonladder Ranking 1 through protocol 21.
That metadata is persisted separately from ephemeral playable rooms. Login also
queries player statistics and clan membership, including valid empty records
for a new account. These steps are required before the online UI completes.

Title session/round AnyData records carry strict type and field layouts. Room
registration assigns authoritative owner and host identities. Browse filters
are decoded into typed comparisons; SQL-like client strings are never executed.
The [coverage inventory](protocol-inventory.md) lists supported SDK operations.

Competition is **protocol 26**, bound by `0x00534610`, request dispatcher
`0x00534f58`, reply decoder `0x005352b8`. 26/1 registers participation; 26/2
associates an owned TTLRound with Ranking 1. The trailing round field observed
as zero is not itself the ranking association. Protocol 15/1 remains a legacy
compatibility alias, not evidence of a full Competition service at that ID.

Round reports combine 15 gathering values and 87 personal values per player.
The host is authoritative for the roster's gathering vector; a participant can
acknowledge the matching aggregate but cannot overwrite another player's data.
Personal writes belong to the caller. Finished rounds retain reporting authority
for two minutes (bounded to 1,024 entries), allowing delayed participant reports
after host departure without restoring join/browse access. Both players' 102-value
records and a subsequent direct-play round were confirmed in game. Original
scoring formulas and complete-match stat-index semantics remain unverified.

## Friends, invitations and clans

The title UI uses protocol 101, not an assumed generic Friends service. All 39
recovered title methods are implemented; see
[friends, clans and statistics](social-and-statistics.md) for layouts and policies.

Consumer `0x00348930` and UI filter `0x00165420` establish friend relationship
bytes **0 incoming, 1 outgoing, 2 accepted**. The original incoming/accepted
encoding was corrected. Retail invite acceptance and subsequent friend queries
passed. Alerts (101/8) carry three lists: friend inviter PIDs, clan invitation
identifiers and game gathering IDs. Completion `0x00344dc0` sets the subsequent
poll interval to 60,000 ms. A game invitation sent with 21/5 was accepted by the
backend; the receiving player confirmed its notification and successful join.
The clan flow was also confirmed by the tester; every clan role/notice branch
has not been individually tested on the retail UI.

## Native joining and relay

Native NAT introduction uses 3/1 from the requester and reliable server-initiated
3/2 to the target. SecureConnection 11/3 resolves a published live room host
and returns bool plus a StationURL list. Dispatcher `0x00551498` and reply decoder
`0x00551900` establish that layout. Native URLs must retain **prudp**; returning
**prudps** incorrectly selects the ticket-manager factory at `0x0054dcd0` and
prevents native session establishment despite successful backend requests.

Forced relay must virtualize early host/participant URLs (21/16, 21/28, 50/4)
as well as 11/3, or the game can connect directly before the later lookup.
Pair-specific virtual endpoints retain player CID/RVCID. Random native USER
challenge echoes prove both endpoint-dependent NAT mappings before forwarding.
Two stock clients confirmed joining, starting, movement and shooting through
the relay. See [connectivity](connectivity.md) for limits and fallback policy.

On host exit, the game sends reliable native leave DATA *after* its backend
quit reply. Closing relay sockets immediately discarded those packets and left
the joiner waiting. Active proved routes now have a fixed five-second drain for
leave DATA and ACKs, with no new admissions. The tester confirmed host quitting
worked after this correction. Listings and invitations still disappear promptly.

## Confirmed behavior and remaining work

Hosted browse captures also established a second stock query ordering:
`WHERE TTLSessions.levelName = '...' AND Gatherings.type = 'TTLSession' AND TTLSessions.isPrivate = 0`.
Requiring the type clause first rejected these map-filtered searches while
ordinary browsing succeeded. The bounded equality parser now accepts either
order, retains the required exact type and rejects duplicates, unknown fields
and unrecognized expressions. Search diagnostics record filters, result IDs
and exclusion reasons without raw room or account text.

Two unmodified PCSX2 Sockets clients confirmed signup/recovery/login, hosting,
browsing/filtered search, native direct and forced-relay gameplay, direct next-round
transition, both players' round reports, host departure, friends, game invite
notification/join and a clan flow. No client patch is used. The verified
two-player startup used both players ready. The original manual specifies that
Force Match Start excludes unready players; the distinct three-player case and
the corrected native validation trace are documented in
[Force Match Start](force-match-start.md). The user subsequently relayed another
player's confirmation that Force Match Start works on RackNerd, without an
additional server change or client patch.

Remaining field checks include physical consoles, blocked-direct adaptive
fallback, forced-relay natural round transition, complete-match reporting,
original scoring formulas and every social/role transition. Unknown SDK methods
fail explicitly; additional metadata does not establish a backend binding.
No game executables, raw decompiler exports, player records or captures are
included in this repository.
