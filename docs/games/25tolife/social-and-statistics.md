# Friends, clans and statistics — SLUS-210.16

Evidence: the retail ELF SHA-256 recorded in [analysis.md](analysis.md), its
embedded DDL, Ghidra request writers and reply dispatcher `0x003592a8`.
Protocol **101** is the title's `TTLMatchMaking` extension. It contains social,
clan, statistics and content operations in addition to session searches.
This is separate from Quazal MatchMaking **21**, Competition **15**, account
management **25**, and the generic Friends protocol found in SDK metadata.
Do not import TDU's custom service numbers into this title.

## Evidence and current implementation

Retail tests cover account signup/recovery, room search/join, friends, game
invite notifications and a clan flow, plus aggregate and personal round reports.
Individual social branches below also have static evidence and handler tests. Numeric request IDs are cross-checked against the
writers and server dispatcher `0x00357780`, rather than assigned solely from
string order. The dispatcher resolves methods 1, 3, 4, 28 and 36, whose public
client writers were absent.
The reply decoder handles cases `0x8001` through `0x8027`.

All **39 title methods** are implemented. Friends, blocks, invitations, clans,
roles, notices, preferences, statistics and report receipts persist atomically
in private `data/state/community.json`. Presence, room participants, invitations
and URLs follow live connections. Text checking accepts nonblank, noncontrol
text; MOTD currently returns an empty message. Account retrieval returns stored
profile/contact data, or unset defaults for an account without a saved profile.
Personal fields are never reconstructed from captures.

Tests cover every method's request/reply envelope and reject trailing input.
An isolated two-account UDP test exercised signup, profile round-trip,
friend invite/accept, clan create/invite/accept, room participation/host URL,
statistics and persistence after a process restart. Retail testing subsequently
confirmed friends, game invite notification/join and a clan flow, as well as
native direct and forced-relay gameplay. Individual role/UI branches still
need broader testing.

Wire notation: `S` is an SDK String (u16 byte length including terminal NUL;
zero length also represents an empty String), `L<T>` is u32 count plus elements,
`B` is one byte, `U` is u32 LE, and `F` is IEEE-754 float32 LE. Concrete records
serialize fields directly. Only polymorphic Gathering AnyData adds a type-name
String, outer Buffer length and inner Data length. A successful RMC envelope
does not imply that the application's returned bool/status indicates success.

## Statistics, filtering and session query methods

| ID | Operation | Request | Reply application fields | Writer |
| --- | --- | --- | --- | --- |
| 1 | FindByLevelName | level S, ResultRange | L<AnyData Gathering> | server case 1 |
| 2 | FindBySQLQuery | query S, offset U, count U | L<AnyData Gathering> | `0x003539f8` |
| 3 | FindByRanking | ranking U, max difference U, ResultRange | L<AnyData Gathering> | server case 3 |
| 4 | FindRankingPosition | player PID U | U | server case 4 |
| 5 | GetTopPlayers | count U | L<TTLStats> | `0x00353be0` |
| 6 | GetStatsForPlayers | L<U PID> | L<TTLStats> | `0x00353da0` |
| 7 | GetLadderStats | pivot PID U, count U | L<TTLStats> | `0x00353fd0` |
| 8 | GetAlerts | none | EidosAlerts | `0x003541a8` |
| 9 | AddStatsPersonal | L<F> | none | `0x00354350` |
| 10 | FilterText | text S | accepted B | `0x00354560` |
| 37 | GetTopClans | count U | L<EidosClanProps> | `0x00357220` |
| 38 | GetMessageOfTheDay | language S | message S | `0x003573e0` |
| 39 | GetEidosAccountInfo | none | private account record, public account record, S | `0x003575a0` |

`FilterText` bool polarity is verified by title wrapper `0x0034ca80`: false
selects error index `0x7c` (invalid filtered text); true advances the request.
The replacement's current policy is structural acceptance; the original
server's profanity dictionary is not recovered.

`TTLStats` decoder `0x0035e190` reads, in order:

1. Player name S and clan abbreviation S.
2. Clan rating F and clan kills F.
3. Player PID U.
4. L<F> statistic values.

Method 6 is **not a friend query**. The retail post-login request contains one
PID (the local account). Accounts without reports return no statistic record.
Existing records contain **102 floats**, required by title consumer
`0x00347be8` (loop `0x65 + 1`).

The reporting state machine `0x0034d628` invokes gathering-level statistics
through `0x0034d9a0`, then personal reporting `0x0034daa8`, then the final
gathering update `0x0034db80`. `AddStatsPersonal` has no target PID argument:
persist under the authenticated caller. Its writer emits a count and float
values, without a qResult payload in the reply. Gathering reports are a
separate Quazal protocol-21 operation and need their own schema/replay model.
Builder `0x0034d738` creates one gathering-stat entry per participant, with
participant PID, a constant type value 1, and **15 float values** copied from
the player's local stat block. Builder `0x0034e0a8` copies **87 personal values**
(loop `0x56 + 1`) from the following block. The runtime accepts exactly these
lengths and finite values. Only the authenticated round host commits aggregate
values for roster members. Every participating retail console submits the same
full vector: participant reports are acknowledged without writing other
players’ totals, and compared with host receipts when available. A participant
may arrive before the host; the host remains responsible for committing the
aggregate. Personal writes always belong to the authenticated caller.

The first retail round at 03:37:51 UTC on November 17 sent both 15-value player
records from both consoles, followed by the host’s 87-value personal report.
The former host-only acknowledgement rejected the joiner’s identical vector;
`0x0034d9a0` treats false as failure and skips the next personal-report step.
The acknowledgement is fixed. Completed TTLRound membership survives only for
reporting for two minutes after host EndParticipation, bounded to 1,024 rounds;
it is absent from discovery, URL lookup and new participation. This handles
independent per-console reporting/exit order. Disconnect removes that exact
connection’s authority. The next retail test passed: host reporting/exit at
04:47:12 UTC, then joiner reporting/exit at 04:47:43–44 UTC, followed by creation
of the next round. Both players’ 102-value records were persisted, and the user
confirmed advancing to the next round. The 31-second gap exercised the completed
round reporting grace rather than only the still-active-round path.

The UI label table at `0x00630690` lists rating, clan rating, sessions, MVPs,
kills, headshots, raid captures, robbery escapes, tags, deaths, suicides,
kill/death ratio, team/clan/NPC kills, won/lost rounds, apprehensions,
apprehended, shots fired/hit, accuracy and most-played mode. **UI table order
is not yet established as wire-vector order.** The replacement retains reported
chunks as snapshots and composes the 15+87 record. Runtime/gathering/PID/chunk
receipts make identical retries harmless and reject conflicting repeats.
The original derived-stat calculations and ranking formula are not recovered.
The current local ranking policy sorts by the first gathering value, ties by
PID, and sums that value for clan rating. Clan-kill summary remains unset until
its mapping is verified. These are replacement policies, not a claim to match
the historical service's scoring formula.

### Browser reply transport limit

The stock UDP transport constructor `0x002daa80` selects a **1,000-byte complete
datagram limit**. `0x002db8a8` copies that budget into its socket, and receive
helper `0x002ef8c0` discards datagrams above it before PRUDP decoding.

A hosted `101/7 GetLadderStats` request for ten rows at 22:14:41 UTC on
November 18 returned nine existing records in a 3,964-byte RMC reply. The former
transport split application data into 1,000-byte chunks, producing three
1,015-byte datagrams and one 979-byte datagram. Only the smaller final fragment
was acknowledged; the remaining three exhausted retries and disconnected the
client from online mode. The stats request itself had succeeded.

25 to Life now caps the complete backend datagram at 1,000 bytes. Each DATA
fragment allows 985 application bytes after accounting for 14 bytes of PRUDP
header, explicit size and checksum, plus the one-byte plain compression selector.
The full statistics list remains intact across fragments. This title policy
does not change the native gameplay relay budget or other adapters' defaults.
Debug logs include response bytes, fragment count and datagram budget when a
response spans packets. Regression coverage uses the retail receive capacity,
reassembles the full reply and checks that acknowledgements preserve the
connection beyond the retry deadline.

## Friends

| ID | Operation | Request | Reply | Writer |
| --- | --- | --- | --- | --- |
| 11 | FriendInvite | player name S | B | `0x00354720` |
| 12 | FriendInviteByID | target PID U | B | `0x003548e0` |
| 13 | FriendAccept | target PID U | B | `0x00354aa0` |
| 14 | FriendReject | target PID U, block B | B | `0x00354c60` |
| 15 | FriendDelete | target PID U | B | `0x00354e38` |
| 16 | FriendsQuery | locale S | L<EidosFriend> | `0x00354ff8` |

FriendData decoder `0x00535ec0` reads PID U, name S, relationship u8, details U,
status S. `EidosFriend` decoder `0x00390cc8` extends it with clan abbreviation S,
L<AnyData Gathering>, and looking-to-play B. Consumer `0x00348930` maps
relationship 0 to OSIPlayer +0xa0, 1 to +0xa4 and 2 to +0x9c. UI filter
`0x00165420` identifies these as incoming requests, outgoing requests and
confirmed friends respectively. The wire values are therefore **0 incoming,
1 outgoing, 2 accepted**. The original incoming/accepted values were reversed;
a live invitation persisted and appeared in replies but selected the wrong UI
category. Pending invitations survive this correction. The remaining details
flags and every UI branch still need verification; retail acceptance passed.

`EidosAlerts`, decoder `0x003901e0`, is three lists of u32 invitation
identifiers: friends, clans and matches. The replacement sends inviter PIDs for
friends/clans and gathering IDs for matches; the clan identifier domain still
needs retail verification. It has no type/length
prefix in the concrete method-8 reply. Retail polls method 8 roughly once per
minute in the observed friends UI. The user confirmed a game-invite notification
and successful join in the next retail test. A friend-request popup has not
been verified.

Implemented persistent model: directed pending invitations, directed blocks and
accepted undirected friend edges. Derive the acting PID from secure proof;
the request PID is only the target. Accept/reject must consume the correct
incoming invitation, and blocks must prevent repeated invitations. This state
transition design has isolated two-client tests; retail incoming-request
acceptance also passed after correcting the relationship values.
Presence/status and active gatherings come from current connections, while
edges, blocks and invitations survive a restart.

## Clans

Clan operations return an application U status first. Title completion helper
`0x00350c68` accepts **0**, unlike the Quazal qResult `0x00010001` used by
authentication/account services.

| ID | Operation | Request | Additional reply fields | Writer |
| --- | --- | --- | --- | --- |
| 17 | ClanGetMyClanInfo | none | EidosClanProps, EidosClanMember | `0x003551b8` |
| 18 | ClanCreateClan | name S, abbreviation S | none | `0x00355398` |
| 19 | ClanGetMyClanMembershipList | none | L<EidosClanMember> | `0x00355570` |
| 20 | ClanGetClanIDListForPlayer | PID U | L<U clan ID> | `0x00355738` |
| 21 | ClanGetClanInfoFromClanID | clan ID U | EidosClanProps | `0x00355918` |
| 22 | ClanGetMyInvitations | none | incoming L<EidosClanInvite>, outgoing L<EidosClanInvite> | `0x00355af8` |
| 23 | ClanInvitePlayer | player name S | none | `0x00355cd8` |
| 24 | ClanCancelInvitation | player name S | none | `0x00355e98` |
| 25 | ClanAcceptInvitation | inviting clan ID U, inviter name S | none | `0x00356058` |
| 26 | ClanRejectInvitation | inviting clan ID U, inviter name S | none | `0x00356230` |
| 27 | ClanBlockInvitation | inviting clan ID U, inviter name S | none | `0x00356408` |
| 28 | ClanSetInvitationStatus | accepting invitations B | none | server case 28 |
| 29 | ClanRemoveMember | target PID U | none | `0x003565e8` |
| 30 | ClanAbandonClan | none | none | `0x003567a8` |
| 31 | ClanPromote | target PID U | none | `0x00356950` |
| 32 | ClanDemote | target PID U | none | `0x00356b10` |
| 33 | ClanNewLeader | target PID U | none | `0x00356cd0` |
| 34 | ClanPostNotice | body S | none | `0x00356e90` |
| 35 | ClanGetMyNotices | none | L<EidosClanNotice> | `0x00357050` |
| 36 | ClanMarkAlertKickedRead | none | none | server case 36 |

`EidosClanProps` (`0x00390b18`): clan ID U, abbreviation S, name S, rank i32,
rating i32, kills U, member count U, leader S.

`EidosClanMember` (`0x00390788`): player PID U, player S, clan abbreviation S,
leader B, officer B, accepting-invitations B, L<AnyData Gathering>, friend B,
online B. Booleans are individual bytes, not u32 values.

`EidosClanInvite` (`0x003905a0`): inviting clan ID U, abbreviation S, clan name S,
inviter player S, clan rank i32, clan members U, invitee player S, read B.
`EidosClanNotice` uses sender and body Strings (decoder `0x00390a10`).
The replacement retains the latest 100 notices per clan.

Error strings are indexed by `99 + status` in `0x00350e28`, table
`0x0059fcf0`. Verified UI meanings:

| Status | Meaning |
| --- | --- |
| 1 | Generic clan failure |
| 2 | No permission |
| 3 | Target no longer in clan |
| 4 | Invalid clan |
| 5 | Invited player does not exist |
| 6 / 7 | Abbreviation / name already taken |
| 8 | Caller already in a clan |
| 9 | Existing or blocked invitation |
| 10 | Target not accepting invitations |
| 11 | Invalid invitation |
| 12 | Leader must transfer leadership or be sole member before leaving |
| 13 | Clan full |
| 14 | Clan name disallowed |
| 15 | Target already in caller's clan |

These errors establish server decisions, but do not establish the original
membership limit, normalization or every role privilege. The replacement uses
case-insensitive unique names/abbreviations, at most 64 members, one active clan
per player, validated invitations and explicit leader/officer checks. Leaders
must transfer leadership to an officer or be the sole member before leaving.
Officers cannot remove the leader or other officers. Each mutation commits
atomically. The size limit and remaining policy choices are local defaults
pending retail verification.

## Validation and remaining evidence

All documented protocol-101 operations are covered by handler/envelope tests.
Two-account integration tests cover invitation/block/accept/delete, clan roles,
leadership transfer, leave/kick, notices, malformed inputs and failed-write
rollback. The isolated UDP test also passed reload of friends, clans, profile
and statistics after restarting the app, without modifying user state.

Retail signup, two-PC joining, gameplay, direct round transition, friends, game
invites and a clan flow passed. Remaining checks include full matches, every
social screen/role transition and the complete stat-index/derived-scoring
mapping. RMC tests alone do not establish untested native/gameplay behavior. Generic SDK protocols whose layouts are not documented here
remain outside this implementation.

The documented room operations now include protocol 21/1 and 2 (register/remove),
4 (update), 5–10 (invitations), 11–16 (participation and participant data),
20–24 (ID/owner/participant/invitation lookups), 26–30 (URL/state) and
31 (host-authenticated gathering reports). Protocol 101/1–3 supplies title room
searches; Competition 15/1 persists membership in shared Ranking 1.

Private local exports: `social-statistics.c`, `social-structs.c`,
`social-statistics-flow.c`, `stats-reporting.c`, `get-alerts-wire.c` (the full
101 reply dispatcher despite its early filename), `ttl-method-writers.json`,
and `clan-bootstrap-fields.c` under `work/25tolife`. They are ignored research
artifacts. This document records layouts and conclusions without copying the
retail binary, account secrets or personal profile fields into source control.
