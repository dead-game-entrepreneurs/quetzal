# Snowblind service and wire inventory

Build/provenance and validation limits: [analysis.md](analysis.md).
This inventory is static evidence from the US PS2 online executable. The
[adapter](running.md) implements the verified subset described below; retail
coverage includes accounts, social flows, matchmaking, relay gameplay and stats.

## Verified protocol bindings

The binding routines below pass the actual protocol number to `0x3009f0`.
It writes the protocol byte through `0x2feec0` before registering the handler.
Do not use the arguments to `ClientService` constructor `0x373ed8` as network
protocol numbers: those are internal client IDs stored at object `+8`.

| Protocol | Family | Registration evidence |
| --- | --- | --- |
| 10 | TicketGranting/authentication | `0x3757f8` |
| 11 | SecureConnection | `0x376320` |
| 21 | MatchMaking | `0x3642d8` |
| 50 | MatchMaking extension | Same binding routine, second protocol object |
| 25 | AccountManagement | `0x360468` |
| 26 | Competition | `0x36e780` |
| 24 | PersistentStore | `0x36fc88` |
| 20 | Generic Friends | `0x359ce0` |
| 22 | Peer | `0x35f290` |
| 101 | Snowblind matchmaking/stats extension | `0x228028` registers object with vtable `0x5fb658` |
| 102 | NetCommClans | Same login routine, vtable `0x5fdb10` |
| 103 | EidosFriends | Same login routine, vtable `0x5fb5c8` |

The NAT client is constructed and bound by `0x228028`. Native binding
`0x35c310`/`0x35cf28` selects protocol 3. Dispatcher `0x35c098` confirms
method 1 takes a StationURL list and method 2 takes one StationURL; both return
no application fields. The shared authenticated traversal service and relay
introductions use these directions.

RMC protocol bytes use the request bit `0x80` (`0x300d40`); method selectors
are serialized through the u32 writer (`0x2ff1d8` -> `0x2f3980`). Replies select
`0x8001` and following method IDs. These agree with the shared RMC conventions;
complete native message, ticket, Any and packet fixtures remain a retail
validation gate. Retail guest/named login, ticket issuance and secure registration now
exercise the shared auth path.

## Native UDP packet layout

The native UDPPacket writer `0x2fd088` writes the 10-byte base header, the
SYN/CONNECT signature or DATA fragment byte, then the transformed payload and
checksum. It does not emit the optional two-byte DATA payload size used by the
newer adapters. Snowblind therefore selects shared PRUDP `ImplicitPayloadSize`;
outbound fragments occupy separate datagrams with `FlagSize` clear.

A stock retry acknowledged the former sized response and advanced the native
reliable receive counter to 2, while its login RPC stayed pending and its reply
buffer remained empty. The extra size bytes were being delivered as payload.
This framing difference is independent of the confirmed plain compression
wrapper. Other adapters retain their existing packet format.

## Custom 101

`S`: SDK String; `U`: u32; `B`: u8; `F`: float32; `L<T>`: u32 count + elements.
Response columns describe application payloads inside successful RMC envelopes.

| Method | Operation | Request | Response / evidence |
| --- | --- | --- | --- |
| 1 | GetTunes | none | `L<F>`; writer `0x15dfc0`, decoder `0x15ef20` case `0x8001`; mandatory in post-login state 2 |
| 2 | GetStats | B selector, S player name, U maximum rows | `L<StatsRow>`; selectors 0 player, 1 top, 2 friends, 3 clan; persisted totals and bounded filtered rows |
| 3 | FindBySQLQuery | S query | `L<Any Gathering + L<StationURL>>`; writer `0x15e358`, called by `0x22fd18` |
| 4 | AddStatsPersonal | U count, repeated S name + F value | no application fields; writer `0x15e518`; validated, round-bound and persisted once |
| 5 | EndParticipation | U gathering ID, S message | bool; writer `0x15e708` |

`StatsRow` field codecs `0x15fa70` / `0x15fb30` contain two Strings, a u32 PID
and a float list. The native menu skips slot 0, reads rank at 1, divides 2/3
for K/D and displays 4/5 as wins/losses. This differs from TTL's row and fixed
personal vector. See the [statistics audit](stats.md) for all 35 named personal
counters, the five-value gathering summaries and remaining aggregation questions.

The search builder requests platform 1, positive player counts and room below
maximum participation. It adds optional type, map and modifier-bit conditions.
Its table/type names are `BO3Matches` / `BO3Match`. Parse a bounded allowed
grammar into typed predicates; never execute client SQL against persistence.
Unlike TTL's 101/2, this writer does not append an explicit ResultRange.

## Custom 103 friends

These are active game wrappers (`0x22a2a8` and following, plus `0x22aab0`),
not only generic SDK definitions. The reply decoder is `0x160c40`.

| Method | Operation | Writer / input |
| --- | --- | --- |
| 1 | FriendInvite | `0x1600a0`: S player name |
| 2 | FriendAccept | `0x160260`: U PID; active wrapper `0x22a668` |
| 3 | FriendReject | `0x160420`: U PID, bool block future requests; wrapper `0x22a808` |
| 4 | FriendDelete/cancel | `0x1605f8`: U PID; wrapper `0x22a4c8` |
| 5 | FriendsQuery | `0x1607b8`: S query/presence selector |

Methods 1–5 are implemented with private tenant `friends.json` persistence.
Methods 1–4 decode bool results. Only the invite recipient can accept/reject;
only its sender can cancel an outgoing request. Either accepted friend can
remove the relationship. Rejection can persist a block against future requests.
Targets must exist in this Snowblind account store, not another game's tenant.
Repeated invites are idempotent and reciprocal requests do not bypass acceptance.
The replacement server bounds pending/accepted rows to 100 per player.

Native row writer/reader **0x15fe28 / 0x15ff10** serialize:

```text
u32 PID, String playerName, u8 relationship,
String presenceMetadata, String clanAbbreviation, L<AnyData> gatherings
```

There is no TTL-style u32 online field or trailing preference bool. Relationships
are **0 incoming**, **1 outgoing**, **2 accepted**. `0x229a98` preserves the byte;
UI `0x1f7ed0` distinguishes invitation/request/friend labels and only treats
accepted entries as online. Three pipe-separated metadata components mark online
details available; current online menu entries use `||` with empty profile
components. Offline entries use an empty String. `0x22a068` exposes the resulting
online/details flag. Clan labels use persisted membership. Gathering attachment
is still empty, so this does not claim working Join Friend session discovery.
`tools/verify_snowblind_friends.py` checks six native row writer/reader cases
and executes the native incoming-only alert predicate with hooked SDK list I/O.

**Notifications use polling:** `0x22aab0` queries the list, `0x223638` detects
an incoming relationship (0), and `0x1ea578` shows/hides the `alert_friends` UI
element. No additional server push is required for that indicator. The backend
returns both sender/outgoing and recipient/incoming rows on polling. Retail
friend requests and acceptance are confirmed; removal/rejection and detailed
alert rendering still need emulator validation.
Generic Friends 20 is a separate binding and must not be aliased to this API.

## Custom 102 clans

The client retains request writers for methods **1–19**, at
`0x16f298`–`0x1712b8`, and reply decoder `0x171f98` with cases
`0x8001`–`0x8013`. The PS2 UI invokes the title's clan facade for:

- Fetching a clan description and the player's current membership/invitations.
- Clan creation, removal/abandonment, promotion, demotion and leader changes.
- Posting notices and marking notices/kick alerts read.
- Invitation preferences, sending/cancelling and accepting/rejecting invitations.

Writers include String identities, concrete clan records and bool preferences;
several query replies return lists or multiple fields rather than one bool.
Observed method 2 is FetchMyClanDesc: no arguments, then result u32,
ClanDesc, alerts, a member list and own-member descriptor. Native 0x173950
writes ClanDesc as two Strings, i32 and a list of two-String notices. Member
writer 0x173ba0 writes two Strings, three bools, a typed-value list and a final
bool. All five outputs are returned even when there is no membership. The
store returns native status **0x68** (NotInClan), mapped to
`clan1_dialog_error_notinclan` by 0x1fa6d8, and empty records/lists for an account
without membership. Members receive status zero, the actual ClanDesc, a
complete roster and their own member record.

Observed method 16 is CheckForAlerts: no arguments, then a u32 result code,
three bools and two Strings (0x173e48/0x173eb0). Pending invitations and unread
notices drive the first two flags. Kick alerts are not implemented.
The subsequent retail Create Clan attempt requested **102/3**, now implemented.
Writer `0x16f660` takes a complete ClanDesc (two Strings, signed
32-bit value and a list of two-String notices); reply decoder `0x171f98` case
0x8003 reads a single u32 application status. Creation requires persistent
membership and nonempty FetchMyClanDesc/member records. The first String is the
abbreviation (UI maximum 3), the second the name (maximum 20), followed by rank
and notices. New clans persist only the authenticated founder/name/tag; client
rank cannot award standing, and injected creation notices are rejected. Status
0xcb means abbreviation taken, 0xcc name taken, 0xcd already in a clan and 0xd4
illegal text, as mapped by `0x1fa6d8`. An identical creation retry is idempotent.
The first member flag marks the leader, confirmed by controls `0x207a18` and
`0x207b60`. The third flag (native +0x16, UI +0x12) controls invitation
acceptance; `0x205e58`/`0x205f98` consume it for the Accepting/Not Accepting
status. New accounts accept invitations by default; **102/10** persists changes. The final member bool (native record +0x24, converted UI record
+0x18) is the online flag: renderer `0x2068c0` selects `clan1_data_online` or
`clan1_data_offline` from it when no gathering is attached. It now follows the
ticket-proven secure registration registry, including disconnect/timeout and
old-generation cleanup. This state is transient; the typed gathering list
remains empty. The native synthetic
description/alert/member fixture is generated by `tools/verify_snowblind_clans.py`.
Notice posting **102/17** is now implemented. Writer `0x170f50` sends one body
String; decoder `0x171f98` case 0x8011 reads a u32 application status. The UI
keyboard in `0x1fd290` limits input to **70 characters**. Notice codec
`0x174028` / `0x174068` writes/reads author and body Strings; populated notices
are included in ClanDesc returned by **102/2**. Posting binds the author and
clan to the authenticated founder, persists atomically, and retains the latest
three notices with newest first (the replacement server's bounded board policy).
Existing version-1 files without the optional notices field remain compatible.

**102/18 MarkNoticesRead** (`0x171110`, called by `0x22e840`) takes no arguments
and returns a u32 status. It clears only that member's unread-notice flag
without deleting notices. Posting marks other members unread; the author's
own posts are read. Native RPC response caching protects ordinary retransmissions
of a post from being applied twice.
Invitation mapping is established by native request writers and active UI jobs:

| Method | Operation | Arguments | Reply |
| --- | --- | --- | --- |
| 4 | CheckMyInvitations (`0x16f820`) | none | U status, L incoming, L outgoing, MemberData own |
| 5 | InvitePlayer (`0x16fa18`) | S player name | U status |
| 6 | CancelInvitation (`0x16fbd8`) | S player name | U status |
| 7 | AcceptInvitation (`0x16fd98`) | S clan abbreviation, S inviter name | U status |
| 8 | RejectInvitation (`0x16ff70`) | same two Strings | U status |
| 9 | AckInvitation (`0x170148`) | same two Strings | U status |
| 10 | SetInvitationStatus (`0x170320`) | B accepting | U status |

Native invitation writer/reader `0x173f10`/`0x173fa0` serialize three Strings
(clan abbreviation, clan name, inviter), i32 rank, U member count, S recipient
and B active offer. Active jobs `0x22c958`, `0x22d590`, `0x22d838`, `0x22d9a0`
and `0x22db08` establish the query/action wiring. The fourth output of method 4
is the complete member record, not a bare acceptance bool. UI actions
`0x2026f0`/`0x201f00` pass abbreviation and inviter from offsets 0/+0x10.

Leaders and Officers can invite. Officers can cancel their own offers; the
leader can cancel any clan offer. The authenticated recipient owns the
accept/reject/ack actions. Acknowledgment clears the invitation alert without
joining or deleting the pending offer. Accepting adds an ordinary member and
removes all offers addressed to that account. Creating a clan also removes its
founder's incoming offers. Invitations, preferences, member IDs and per-member
unread notice IDs persist atomically in the backward-compatible version-1 file.
The server bounds each clan to 64 members and the tenant to 4096 pending offers;
these are replacement-server limits. Terminal invitation history is not retained.
Native codecs and synthetic lifecycle/reload/error tests pass. Retail testing
confirmed invitation queries, sending, acceptance and the updated member roster;
rejection/cancellation, preferences and detailed alert rendering remain unchecked. Other clan mutations remain unsupported.

Clan role operations are **102/13 Promote** (`0x170848`, active job
`0x22e000`) and **102/14 Demote** (`0x170a08`, active job `0x22e148`). Both
write one player-name String; decoder `0x171f98` cases 0x800d/0x800e read a
single u32 status. Job labels at `0x62f818`/`0x62f840` identify the operations.

Native member +0x15, converted UI +0x11, is the Officer bool. Member writer
`0x173ba0` and reader `0x173ca0` are exercised with an Officer fixture as well
as leader and Soldier cases. UI `0x207a18`/`0x207b60` distinguish leader,
Officer and ordinary-member permissions. Promotion/demotion are leader-only
and preserve membership; Officer recruiting and notice privileges are enforced
by the server, with private persistent Officer IDs. Existing state needs no
migration. An Officer's prior invitation remains valid after demotion while its
sender remains in the clan. **102/15 NewLeader**, kick and leave remain separate
unsupported mutations. Native/synthetic role tests pass. The tester confirmed
Promote/Demote in the retail client, with completed **102/13** and **102/14**
calls. Officer recruiting/notices still lack a corresponding retail call in
this test.

The TTL community engine's authority/persistence concepts are reusable, but
its method IDs and concrete clan codecs are not a Snowblind API.

## Gathering and account codecs

`BO3Match` is registered as a concrete prototype by the game. The field writer
is `0x15dd10`, reader `0x15dd88`; virtual wrappers are `0x15df68` / `0x15df90`.
The writer first calls common Gathering serialization `0x365848`, then emits:

| Native offset | Wire width | Current interpretation |
| --- | --- | --- |
| `+0x3c` | B | Platform, initialized to 1 by the host job; search also requests platform 1 |
| `+0x3d` | B | Copied from host-job `+0x10`; game-type interpretation needs final caller correlation |
| `+0x3e` | B | Low byte of host-job options at `+0x14` |
| `+0x3f` | B | Following options byte at host-job `+0x15`; finish modifier-bit mapping |
| `+0x40` | B | Initialized to 1; player-count interpretation needs dynamic-update correlation |
| `+0x44` | U | Value dereferenced through host-job `+0x2c`; expected map ID still needs caller correlation |

These initialization writes are in `0x22ec20`; do not assign remaining field
semantics solely from the order of SQL column names. The common base is ID,
owner PID, host PID, min/max u16, four u32
policy/argument/flags/state values, and a description String. Its layout matches
the shared Gathering field codec. Native object sizes/padding are not wire sizes.
The native synthetic field trace now checks writer/reader symmetry. Typed
Any/Data envelopes are bounded; retail lobby/round entry is now confirmed.

`BO3Stats` and `BO3Round` are descriptions, not additional concrete type names:

- Post-login constructs SDK **Ranking** (type-name function `0x36f7e8`), sets
  description `BO3Stats`, registers it and calls Competition host registration.
  Ranking adds a String and a list of typed column definitions; see `0x36e970`.
- Round creation `0x2314b0` constructs base **Gathering**, sets description
  `BO3Round`, registers it, associates it with the ranking, participates,
  launches the native session and sets state 2.
- Round exit `0x232188` changes state, submits aggregate reports, submits named
  personal values through 101/4, then ends participation through 101/5.
  Gathering-stat writer `0x36dbc0` emits two u32s and a float list per record.

Eidos profile field codecs are also close to TTL: public writer `0x1615c0`
adds one u32; private `0x161310` adds one byte, a String and a date converted
to an eight-byte value. Reader `0x161370` reverses it. The startup adapter uses
the shared bounded Eidos validator from TTL. Complete Snowblind profile envelopes passed the shared validator during the retail
signup flow; date interpretation remains opaque.

## Remaining validation gates

Synthetic UDP clients exercise each explicitly configurable payload mode.
Retail startup confirms UDP 13015/13016, the plain compression wrapper, checksum
seed 0x02, implicit DATA sizes, guest/named login, ticket proofs, secure
registration, profile/contact updates and BO3Stats bootstrap.

Remaining checks include wider existing-patch/memory-card states, tuning
values beyond shipped defaults, ranking columns, private-room rules and
Internet NAT combinations. BO3Match/round entry, full reports, populated stats
menus and native peer framing are now retail-confirmed. Friends
103/1–5 persist friendship state. Clan queries 102/2, 102/4 and 102/16,
creation, invitations 102/5–10, additional members and notices are implemented.
Promotion/demotion 102/13–14 are implemented and retail-confirmed.
Kick/leave and leadership transfer remain unimplemented. Shared code
should gain explicit policies only where verified differences require them.

## Matchmaking implementation

The adapter handles common 21/1,2,4,11,12,13,15,16,21,26,27,28,29,30,
50/1–4, round-to-ranking association 26/2 and participation exit 101/5.
Native host startup uses 21/21 (FindByID), 21/11 and 21/26 (LaunchSession);
wrappers 0x3644c0, 0x364598 and 0x364758 reach those exact writers. The native
50 decoder is 0x36cf70; participants are u32 lists, detailed participants add
name and message Strings, and 50/4 returns gathering IDs with URL lists.

Search row writer/reader 0x15fc38/0x15fcf0 append URLs after the typed gathering.
The registry binds ownership to PID plus exact secure peer generation, enforces
capacity/owner mutations, normalizes native RVCID, sorts results and removes
owned lobbies/rounds on disconnect. Rounds inherit their owned staging lobby's
capacity and are removed with it. Shared authentication, URL parsing and native
NAT requests are reused. This is an implementation inventory, not an assertion
that every method has been exercised by a retail client.

Retail local testing verified Friends query 103/5, clan alerts 102/16, lobby
registration/lookup/participation/URL publication, BO3Round creation, ranking
association and state 2 (active round). The clan menu additionally requested
102/2, which is now handled with its native no-membership response. Matchmaking
browser search, a second player's join and round entry are now confirmed on
LAN. Server logs show both accounts participating in the lobby and its round;
Internet NAT remains unverified; LAN adaptive relay is confirmed.
All four stats menus now open in the retail client with the empty-state query
handler. Summary/personal uploads, durable deduplication and populated menu
queries are implemented. A completed two-player relay round verified both
summary uploads, both full 35-field personal maps and EndParticipation;
the tester also confirmed populated menu display.
**21/31** is `U gathering, L<U PID, U category, L<F>>` with a bool reply.
The receipt store awards only the authenticated reporter's own row and retains
raw roster copies. **101/4** has no application output fields and binds to the
previously accepted summary on the same registered connection. See
[stats persistence and ranking policy](stats.md#upload-persistence-and-deduplication).


## Adaptive relay integration

Browser **101/3**, participant **21/16**, batch **50/4** and CID **11/3** URL
lists now publish caller-specific direct/virtual candidates through the shared
native relay. **21/28** selects the first candidate for its single String output.
Probe method **3/1** accepts only an issued virtual endpoint for that exact
control generation or an accessible direct endpoint (direct bypass is disabled
in forced-relay mode). Counterpart **3/2** instructions use the issued virtual
URL. All endpoint proofs and opaque forwarding remain in the shared engine.

Native `0x35c6a0`/`0x35c7d0` establish the 13-byte NAT payload; `0x35cdc0`
establishes the address/RVCID cache key, explaining the virtual-only policy for
same-IP candidates. The native payload fixture and synthetic UDP fallback tests
pass. Stock-client captures now confirm seed-0 native peer framing. Browser
discovery and a blocked-direct adaptive LAN join passed after correcting
mixed probe introductions for a requester sharing the relay IP. The same relay carried a completed round and both clients’ full stats uploads.
Same-IP hosting, host departure and Internet NAT remain unverified. See the [relay test guide](running.md#adaptive-relay).
