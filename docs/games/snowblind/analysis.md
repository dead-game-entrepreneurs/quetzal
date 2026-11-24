# Project: Snowblind research record

US PS2 `SLUS-210.37` uses Quazal Rendez-Vous/NetZ in `BO3NETGM.ELF`.
Research used the private extracted ELF and Ghidra; no executable or decompiler
exports are distributed. Shared Quazal authentication, Eidos account data,
Ranking metadata and native relay primitives are reused with title policy.
See the [run guide](running.md) for current setup, the
[protocol inventory](protocols.md) for wire contracts and
[statistics inventory](stats.md) for counters, sources and persistence rules.

The sections below record findings as testing progressed. Early pending checks
are superseded by the dated results at the end: a completed two-player relay
round, full reports, populated stats menus and clan promotion/demotion have
since passed. Remaining gaps include Internet NAT, same-IP player hosting,
host departure, original ranking math and the unsupported clan mutations.

## Retail lobby and round validation

The tester loaded Friends, created a 16-slot BO3Match and started a round.
The backend observed 21/1, 21/21, 21/11 and 21/26 for the lobby, followed by base
Gathering creation, 26/2 competition association, participation, URL publication
and 21/30 state 2 for the round. No unsupported matchmaking calls occurred in
that host path. This validates local host startup, not a second player's join.

The reported clan-menu search error was a newly observed FetchMyClanDesc
102/2, rather than matchmaking search 101/3. Its complete five-output schema
has now been traced and implemented for an account without a clan. Native
status 0x68 identifies NotInClan; this does not create a clan or membership.

## Statistics menu and reporting audit

Retail testing subsequently hit GetStats **101/2** for Top Players, Player
Search, Friends and Clan statistics. All four now open with the native
empty-state reply handler. The [statistics inventory](stats.md) documents the
six values consumed by the menu, 35 named personal fields uploaded through
**101/4**, five-value player summaries through **21/31**, round-leave ordering,
native synthetic verification and the new receipt-based persistence/aggregation.
The summary and personal handlers now acknowledge validated uploads and derive
menu totals from durable round/player receipts; populated retail menus and the
complete leave pipeline still need validation.
The client can use an empty category-2 report for nonreporting round exits;
normal reporting requires at least two queried roster entries.

A later clan-creation attempt hit **102/3**, independently of the successful
stats-menu reads. Its native writer is `0x16f660` and response is a u32 status.
Creation now persists the authenticated founder, abbreviation and name;
**102/2** returns the complete populated descriptor/roster/own-member response.
Native writer/reader fixtures, persistence/reload, uniqueness, retries,
concurrent name collisions and rejected/failed writes are tested. The first
member flag maps to leader controls (`0x207a18`, `0x207b60`). Retail testing
confirmed creation and the one-member roster, then exposed the final bool being
left false (Offline). Renderer `0x2068c0` reads UI member +0x18, populated from
wire/native member +0x24; it now follows secure registration/disconnect state.
Native codec fixtures cover both online/offline replies and tests cover fresh
registration, disconnect, stale disconnects and unchanged persistent membership.
The corrected label still needs retail confirmation; other social mutations
are unimplemented.

## Clan notices

The next retail failure was **102/17 PostNotice**. Ghidra writer `0x170f50`
emits one String, and the reply is a u32 status. The keyboard limit in
`0x1fd290` is 70 characters. Native notice writer/reader `0x174028` / `0x174068`
serialize author then body. The handler now persists the latest three notices,
binds posts to the authenticated leader/Officer's clan and returns them in **102/2**.
**102/18 MarkNoticesRead** has no arguments and clears that member's unread marker. Empty old state files remain compatible. Native fixtures,
reload, complete reply parsing, membership separation, text/size limits,
retention and failed writes are tested; retail posting still needs confirmation.

## Friends and notifications

The second PC's successful Snowblind account reached unsupported **103/1**.
The adapter now persists requests, accepted friendships and rejection blocks in
`friends.json`; both perspectives are returned by **103/5**. The confirmed
method mapping is **1 invite, 2 accept, 3 reject with block bool, 4 delete/cancel,
5 query**. Earlier operation labels for 2–4 were provisional; active wrappers
`0x22a668`, `0x22a808` and `0x22a4c8` establish the corrected mapping.

Native friend codecs `0x15fe28` / `0x15ff10` have PID, name, one relation byte,
two Strings and a gathering list, without TTL's extra fields. **0 incoming,
1 outgoing, 2 accepted** is preserved by `0x229a98`. `0x223638` finds incoming
rows and `0x1ea578` toggles the client Friends alert; notification delivery is
the normal polled query flow. Profile fields use empty values
in the native three-component metadata format, and no gathering is attached.
The implementation binds actions to the authenticated account and requires
recipient consent, limits each list, persists atomically and exposes current
secure-registration presence. Native fixtures and synthetic lifecycle, reload,
blocking, membership/privacy and failed-write tests pass. Friend requests
and acceptance are retail-confirmed; removal/rejection and detailed alert rendering
remain to be checked.

## Clan invitation flow

The next retail clan-invite attempt failed at **102/4 CheckMyInvitations**,
before sending the invite itself. Native `0x171f98` case 0x8004 requires four
outputs: status, incoming invitations, outgoing invitations and own MemberData.
The request/action writers establish **5 invite, 6 cancel, 7 accept, 8 reject,
9 acknowledge, 10 acceptance preference**. Invitation codec `0x173f10` /
`0x173fa0` and member codecs are executed in the native synthetic harness.
UI `0x205e58`/`0x205f98` also establish that member flag three controls the
Accepting/Not Accepting invitations setting.

These operations now persist pending offers and preferences, authorize sender
and recipient actions, and add accepted ordinary members to both clan and
social/stat labels. Acknowledgment clears the alert but leaves the offer;
acceptance clears competing offers. Notice-read state is tracked per member.
Existing founder-only files load unchanged. Synthetic codec, permission,
rollback and restart tests pass. The subsequent two-client retail test confirmed
sending, acceptance and the updated member roster. Rejection/cancellation,
preferences and detailed alert rendering still need retail checks.


## Two-player LAN verification

The same test reached a game successfully after clan acceptance. Server logs
show the second account searching through **101/3**, joining the BO3Match lobby
through **21/11** (two participants), then joining its round (two participants).
This confirms local browser search and second-player lobby/round entry alongside
the reported in-game result. During transition the joining client also attempted
**21/4** with the host's gathering identity; the owner-only update check rejected
it, and round participation succeeded afterward. That nonfatal call remains
recorded for further native tracing; host mutation authority is unchanged.
Internet NAT/relay, sustained gameplay and result uploads still need testing.


## Statistics uploads and leaderboards

Implemented **21/31** and **101/4**, with a shared atomic tenant-state writer
also used by friends/clans. Private `stats.json` receipts deduplicate each
server-nonce/player and retain personal counters separately from scoreboard
summaries. Only a reporter's own row contributes totals; copied rows cannot
award the other players. Exact-generation round participation, BO3Stats
association, bounded fields and map dimensions are checked. Late participant
uploads survive host cleanup, and renewed round epochs do not reuse dedup keys.

The GameFAQs walkthroughs and multiplayer class/weapon FAQ contained no rank
formula. The official manual confirms neighbor-window Player Search and best-15
Friends/Clan menus; those behaviors now query earned persisted totals. The
provisional server ordering is wins, kills, fewer deaths, PID. Neither the
original Snowblind overall score nor 25 to Life's rating formula is claimed.
See [stats.md](stats.md) for sources and the aggregation/deduplication contract.
Native menu checks, Go lifecycle/reload/conflict/rollback/filter tests pass;
full retail uploads and populated displays remain the next check.


## Adaptive relay audit

Ghidra confirms the native protocol-3 list/single-URL contract and the shared
13-byte NAT payload. Native routine execution verifies the writer/reader field
widths (1,4,8), and the cache lookup `0x35cdc0` plus address accessor `0x3164c8`
confirms same-IP/different-port candidates collide under the same RVCID. The
adapter therefore shares Commandos' virtual-only same-IP compatibility policy.

The shared relay core handles proof, SYN activation and opaque forwarding;
Snowblind supplies room/caller authorization, URL-list injection, CID lookup,
probe introductions and room/round/disconnect cleanup. Default reserved pool is
UDP 30800–30927. Synthetic routing/native UDP tests and the original payload
codec check pass; the blocked-direct LAN fallback join now passes. Same-IP hosting, Internet
NAT combinations and full-round validation remain pending. The later bounded firewall tests and cleanup are recorded below.


### Retail introduction-cache correction

A direct two-PC join passed with native peer checksum seed 0. The subsequent
blocked-direct attempt failed before any alternate SYN; only the caller's relay
mapping was proved. The host received both direct and virtual requester
instructions with the same IP/RVCID and kept probing the direct port. Ghidra
`0x35b4a8` confirms an existing cache record is refreshed without replacing its
URL. Mixed probe batches now suppress the direct requester introduction when
requester IP equals relay IP, leaving one virtual counterpart instruction.
The code retains direct-first host candidates and direct-only probe behavior.
Synthetic regression checks pass; the corrected emulator retry also passed
with the direct-only block still active.
The temporary direct-only block was explicitly removed and absence verified.


### Corrected relay retry result

Fresh clients reproduced the same topology and direct-UDP block. Only a
virtual counterpart instruction reached the host; both mappings proved, and
the connector selected the relay with a native alternate SYN. The tester
confirmed game entry. During the block, one active pair had two proved
mappings and 220 forwarded packets / 10,523 bytes. Direct output counters
showed 25 dropped packets. The firewall table was removed and its absence
verified; the server and established relay stayed running. The
[running guide](running.md#successful-adaptive-retry-2026-11-23-chicago)
records the timing and remaining gameplay/stats/Internet checks.


### Full retail result reporting, 2026-11-24

The completed relay round produced successful **21/31**, **101/4** and **101/5**
from both accounts. Each personal map contained all 35 audited names. One
carbine kill/win and one death/loss match the saved summaries. Reloading actual
saved state and replaying its receipts into a temporary copy did not duplicate
awards. Native leaderboard rows for Top, Friends, Clan and Player Search match
those totals and current memberships. The tester subsequently confirmed
all four populated stats menus.
The relay remained active across the round and into the next one; the snapshot
check did not restart the server or write to live account/social/stat files.


## Clan role follow-up, 2026-11-24

The tester confirmed populated stats menus, then hit **102/13** on Promote.
Ghidra establishes method 13 as Promote and 14 as Demote, both single-name
requests returning u32 status. The member's second bool is Officer; the first
remains leader and the third remains invitation acceptance. Officer roles now
persist and appear in both roster and own-member records. Only the leader
can change roles; Officers gain recruiting and notice privileges and lose them
on demotion. Role persistence, native complete replies, authority boundaries,
invitation ownership and failed writes are tested. The tester confirmed the
retail retry worked; logs show Promote **102/13** then Demote **102/14**
completed successfully. No Officer notice call appeared in that retry, so its
authorization remains synthetically tested.
