# Project: Snowblind run guide

US PS2 `SLUS-210.37` only. The experimental adapter supports accounts,
Eidos profiles, BO3Stats bootstrap, matchmaking, persistent friends/clans,
receipt-based statistics, leaderboards and adaptive relay. Stock PCSX2 testing
confirmed two-player LAN entry, a blocked-direct relay join, a completed relayed
round with both full reports, populated stats menus and clan promotion/demotion.
Host migration, clan kick/leave and leadership transfer are unimplemented.
Internet NAT and physical consoles remain unverified. Unsupported RPCs return
errors and are logged.

Enable alongside existing tenants:

```dotenv
QUETZAL_GAMES=25tolife,tdu,wcc,commandos,snowblind
QUETZAL_RELAY_FIRST_PORT=30400
QUETZAL_RELAY_LAST_PORT=30527
QUETZAL_SNOWBLIND_PAYLOAD_MODE=plain
QUETZAL_SNOWBLIND_RELAY_MODE=adaptive
QUETZAL_SNOWBLIND_RELAY_FIRST_PORT=30800
QUETZAL_SNOWBLIND_RELAY_LAST_PORT=30927
```

Snowblind binds **UDP 13015** (auth) and **13016** (replacement secure endpoint),
with isolated accounts, keys, ranking, friends, clans and stats in
`tenants/snowblind/`. Adaptive relay reserves **UDP 30800–30927** by default;
allow/forward that pool on the server alongside the backend ports. The auth reply
advertises the actual secure port. `QUETZAL_SNOWBLIND_BIND`, `PUBLIC_IP`,
`AUTH_PORT` and `SECURE_PORT` override those title settings. `PAYLOAD_MODE`
accepts `plain` (default), `rc4`, `raw`. The US retail guest-login capture uses
the plain compression wrapper, protocol 10/1, and checksum seed 0x02 from
`33ggb39`. Outbound packets use the older implicit payload-size format; newer adapters
keep their explicit size field. Guest/named login, service tickets, secure registration, account signup,
profile/contact/status updates, GetTunes and Ranking bootstrap are retail tested.

## DNS and HTTP

Point these names at the reachable server IPv4:

```text
sbusps2.quazal.net
sb1.patch.eidos.com
sb2.patch.eidos.com
sb3.patch.eidos.com
```

Example: [snowblind.dns.yaml](../../../configs/snowblind.dns.yaml). Preserve
separate DNAS forwarding. The online executable requests `/psb1a/scea.txt`
over **TCP 80**, so proxy `/psb1a/` to the Quetzal HTTP listener (default 8082),
or serve the exact text response below from the existing web server:

```text
http://sb1.patch.eidos.com/psb1a/disabled
0
```

The handler sends CRLF line endings, HTTP 200 and no-store. Its zero checksum
matches an absent local patch, and leaves the shipped executable/assets intact.
`tools/verify_snowblind_startup.py` runs the native manifest parser and patch
state decisions with synthetic HTTP, idle UI, absent local storage and no
`forcedownload` flag. It hooks libc atoi and translates low-word EE register
operations for Unicorn. This checks that branch, not every existing-memory-card
patch state or a stock emulator boot. A legacy literal-IP URL in GAME.INI is
still under investigation; do not assume DNS covers it.

## Verification

Use the resolver above and fully restart the game. Open Online, create a fresh
account, then log in. Inspect `/games/snowblind/connectivity`, tenant logs and
private captures. Keep captures private because account/profile fields can be
present. After a backend restart, leave Online completely and reconnect both clients;
reboot without save states if a stale room remains cached. The native harnesses
below require your privately extracted US ELF and Python Unicorn:

```sh
scripts/go test ./...
scripts/go vet ./...
python3 tools/verify_snowblind_startup.py
python3 tools/verify_snowblind_gathering.py
python3 tools/verify_snowblind_stats.py
python3 tools/verify_snowblind_clans.py
python3 tools/verify_snowblind_friends.py
python3 tools/verify_snowblind_nat.py
```

[Analysis](analysis.md) and [protocol inventory](protocols.md) distinguish static
evidence, native synthetic tests and the remaining runtime checks.

## Social queries and matchmaking

Friends **103/1–5** now support sending, accepting, rejecting/blocking, cancelling
and removing requests, with per-tenant `friends.json` persistence and native
query rows. Both accounts must exist in Snowblind. Only the recipient can accept
or reject. Incoming rows drive the client's polled Friends alert. Online labels
follow secure registrations; gathering attachments/Join Friend are pending.
Native row codecs and synthetic flows are tested. Retail testing confirmed
friend requests and acceptance; rejection/removal still need retail checks.

Clan alerts **102/16** return the native alert record, including pending
invitation and unread-notice indicators. Clan descriptor query **102/2** returns native NotInClan
(0x68) plus all five required output records for an account without membership.
Clan creation **102/3** now persists the abbreviation, name and founder in
private tenant `clans.json`; FetchMyClanDesc returns the complete descriptor,
founder roster and own-member record after creation, including after restart.
The UI's three-character abbreviation and twenty-character name limits apply.
Names/tags are unique without regard to case, and a creator can belong to one
clan. Client-supplied rank is ignored; new clans are unranked.

Clan invitations **102/4–10** persist in the same private state file: query
incoming/outgoing, invite by player name, cancel, accept, reject, acknowledge
and set acceptance preferences. Leaders and Officers can invite; Officers
can cancel their own offers, while the leader can cancel any clan offer. Only
the recipient can accept/reject. Acceptance adds an ordinary member and clears
other offers to that player, maintaining one clan per account. The invitation
alert is polled through **102/16**; acknowledging it does not accept the offer.
Reconnect both clients, invite from the founder, then refresh the recipient
Clans/Invitations screen and accept. Refresh Members on both clients afterward.
Native codecs, permissions, preferences, failed writes and reload are tested;
retail testing confirmed sending, acceptance and the updated member roster.
Rejection/cancellation, preferences and detailed alert rendering still need
retail checks. Promotion/demotion now persist Officer roles, described below.
Kick/leave and leadership transfer are not enabled yet.

Leaders and Officers can post notices through **102/17**. The latest three notices,
including authenticated author names, persist in the same `clans.json` and
appear when the clan description reloads. The native UI permits 70 characters.
**102/18** clears only the authenticated member's unread-notice alert;
the author's own posts are already read. Native notice codecs and persistence are tested;
retail posting/rendering still need confirmation.

Retail testing confirmed creation and the founder roster. The member's
Online/Offline flag follows secure registration and disconnect; it is not
stored in `clans.json`. Gathering attachments/Join Friend remain pending.

Matchmaking has an isolated transient registry for BO3Match lobbies and base
Gathering rounds described as BO3Round. Search **101/3** evaluates only the
retail builder's bounded platform/type/map/modifier predicates; it never runs
client SQL. Each result contains a typed gathering and a StationURL list,
matching the native decoder. Only a ticket-proven owner can publish or update
its lobby; addresses/CID/RVCID claims are checked against the live registration.

Reconnect after a server restart, host a lobby, search from the second PC and
try joining. Check player counts, map/mode changes, leave/re-entry and host
cleanup. Local retail hosting, browser search, a second player joining and
round entry are verified on LAN. Internet NAT behavior still needs validation. `/games/snowblind/connectivity` reports
transient gathering count, configured connectivity mode and relay counters.

All four statistics menus are retail-tested with empty-state replies. The
updated server now accepts **21/31** summaries followed by **101/4** personal
counters, then **101/5** EndParticipation. Per-tenant private `stats.json` keeps
durable round/player receipts; identical reports do not add totals twice.
Top Players uses earned totals, Friends filters accepted friendships, Clan
filters actual members, and Player Search shows ranked neighbors or an unranked
baseline. Clan labels follow current membership.

Reconnect both clients after deployment, play a two-player round with Record
Stats enabled (preferably one kill/death), finish it and return to the lobby.
A completed two-player relay round now confirms both summaries, both full
35-field personal maps and EndParticipation. Saved-state reload, duplicate
replay on a copy and native leaderboard row checks pass. All four populated stats
menus are retail-confirmed.
Current rank ordering is a documented replacement policy, not recovered original
skill math. See the [statistics inventory](stats.md) for evidence, deduplication,
state limits and unresolved result semantics.


## Adaptive relay

Snowblind reuses `internal/protocol/quazal/peerrelay` alongside 25 to Life and
Commandos. Its tenant defaults are independent:

| Setting | Default | Behavior |
| --- | --- | --- |
| `QUETZAL_SNOWBLIND_RELAY_MODE` | `adaptive` | Direct first, then virtual fallback; `direct` disables it, `relay` forces virtual endpoints for diagnosis |
| `QUETZAL_SNOWBLIND_RELAY_FIRST_PORT` | `30800` | First reserved server UDP port |
| `QUETZAL_SNOWBLIND_RELAY_LAST_PORT` | `30927` | Last reserved server UDP port; two sockets per pair |

All title/backend and tenant relay pools must be separate. Startup rejects
collisions. Peer endpoints are allocated on demand; clients establish mappings
outbound. This selects a relay during connection establishment, and does not
migrate an established direct connection after a mid-match failure.

StationURL lists in browser results **101/3**, participant URLs **21/16**, batch
URLs **50/4**, and CID lookup **11/3** offer direct then virtual candidates for
each accessible remote player. The single-host query **21/28** returns the
first configured candidate. The player's own registered URL stays unchanged.
Virtual URLs preserve PID/CID/RVCID and native stream/session fields. Browser
access permits a published host; participant addresses require shared room
membership. Virtual probes are tied to the caller's exact control connection
and are checked against the live room again before counterpart introduction.

The native probe cache `0x35cdc0` compares **address plus RVCID**, ignoring UDP
port (`0x3164c8` reads the address field). Thus a player whose direct IP equals
the relay IP receives only its virtual candidate in adaptive mode. Other
players retain direct-first behavior. This is the same compatibility policy as
Commandos, now supported by Snowblind's native cache analysis.

Browser probes can prove mappings and queue counterpart instructions, but do
not activate adaptive gameplay forwarding. A native stream-1 SYN selecting the
virtual endpoint activates it; both sides must answer independent nonce echoes.
The shared engine forwards opaque peer packets and retains its existing rate,
buffer, candidate-expiry and short leave-drain rules. Room/round removal and
exact-generation disconnects release the title's routes.

`GET /games/snowblind/connectivity` exposes `peer_connectivity` and `relay`:
mode, candidates, active, draining, proved mappings, forwarded packets/bytes.
Candidate publication is not evidence of a relayed join. Look for an active
pair, two proved mappings and increasing forwarded traffic.

### Validation

Ghidra exports are private under `work/snowblind/relay-*.c`. Native protocol 3
binding is `0x35c310`/`0x35cf28`; dispatcher `0x35c098` confirms list method 1
and single-URL method 2, both empty replies. NAT stream constructor `0x35c5c8`
selects stream 5/port 1. Writer `0x35c6a0` and reader `0x35c7d0` use the shared
13-byte kind/u32 RVCID/u64 timing-echo payload. Native payload execution with
hooked SDK helpers is reproducible using:

```sh
python3 tools/verify_snowblind_nat.py
```

The native harness checks the payload, not the complete UDP checksum or fallback
timing. Go tests exercise synthetic stream-5 probes and stream-1 SYN/data with
seed 0 through the shared relay, candidate ordering, same-IP/forced/direct modes,
lookup authority, fresh/stale generations, nested room cleanup and pool conflicts.
The retail peer capture now confirms seed-0 stream-5/stream-1 framing, and
the corrected build passed the blocked-direct LAN join described below.

To reproduce the LAN fallback test, host on a different PC from the backend.
Establish a direct baseline, leave, then temporarily block only observed direct
peer UDP while keeping backend/relay ports reachable. Refresh and rejoin, then
check alternate SYN, both mapping proofs, forwarded traffic and game entry.
Remove the temporary block afterward. No test firewall rules remain installed.


The [research record](analysis.md#retail-introduction-cache-correction) explains
the initial failed fallback and the native cache correction used by this build.

### Successful adaptive retry, 2026-11-23 (Chicago)

With fresh connections, the same second-PC host and backend-PC connector,
the timed UDP-4659 block was reinstalled. The mixed **3/1** probe batch queued
zero direct requester introductions and one virtual counterpart instruction.
Both sides proved their relay mappings at **05:59:05–06 UTC on November 24**.
The connector joined the lobby at 05:59:09 UTC, then selected the alternate
with a native stream-1 SYN at **05:59:24 UTC**, about 15 seconds later. Round
participation reached two players at 05:59:30 UTC, and the tester confirmed
loading into the game.

While the block was still installed, diagnostics showed **one active pair,
two proved mappings, 220 forwarded packets and 10,523 forwarded bytes**. Both
directions had increasing traffic. The firewall counter recorded **25 dropped
outgoing direct packets**; the incoming direct counter stayed zero because the
host used its virtual counterpart instead. The capture contains stream-1
handshake/DATA through ports 30800/30801, with native seed 0.

The block was then explicitly removed, its table's absence verified, and the
private capture stopped. The server stayed in adaptive mode with the existing
relay route active and traffic continuing. This proves native adaptive relay
selection and in-game entry for the tested LAN topology. The same pair subsequently carried the completed round described below.
Same-IP player hosting, host departure and Internet NAT remain unverified.


### Completed relay round and reports, 2026-11-24

The tester completed the subsequent two-player round. Both clients sent full
category-1 summaries, 35-field personal maps and EndParticipation successfully.
The host's one carbine kill/win and the connector's one death/loss agree across
summary and personal receipts. A read-only saved-state check confirmed one
award per reporter and correct native leaderboard rows, including social filters.
See the [completed-round statistics evidence](stats.md#completed-relay-round-2026-11-24).

The same relay pair stayed active through the round, with two proved mappings
and **4847 forwarded packets / 118756 bytes** at the post-round observation.
The backend continued hosting the next round. This adds completed-round and
result-upload coverage to the blocked-direct join test. Populated stats menus
are now retail-confirmed. Same-IP player hosting, host departure and Internet
NAT cases still need checks.


## Clan promotion and demotion

The member-list Promote attempt reached unsupported **102/13**. The adapter
now implements **13 Promote** and **14 Demote**, each one player-name String
with a u32 application status. Only the current leader can change roles, and
the target must be another member of that same clan. Promote makes a Soldier
an Officer; Demote returns an Officer to Soldier. Retrying the same desired
role is idempotent. Founder/leader identity and membership do not change.

Officer IDs persist as an optional version-1 `officers` field in `clans.json`;
existing files load unchanged. Both roster and own-member replies set the
native Officer bool. Officers can recruit and post notices, can cancel their
own pending invites, and cannot change other players' roles. Offers issued
while authorized remain valid if their inviter is later demoted but still a
member. Demotion immediately removes recruiting and posting privileges.

Native codecs, permission/foreign-target checks, reload, repeated actions and
failed saves are tested. Reconnect both clients, promote the other member,
refresh Members on both sides, then test Officer recruiting/notices and Demote.
The tester confirmed Promote and Demote both worked; logs show completed
**102/13** followed by **102/14** on 2026-11-24. Officer recruiting/notice
authorization is covered by tests; no Officer PostNotice call was observed in
this retry. Promoting an existing Officer
to leader invokes the distinct **102/15 NewLeader** call, which remains
unsupported alongside kick/leave administration.
