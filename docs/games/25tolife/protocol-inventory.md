# Protocol coverage — SLUS-210.16

Each `protocol/method` pair identifies an RPC operation. The server currently
uses the generic failure result `0x80010002` for unsupported operations. Retail
UI messages can also describe application-level false/status results or local
client failures, so an error message alone does not identify the missing method.

This inventory covers verified dispatch tables and current routing. It is not
a claim that every SDK method is exercised by this title. The 39 title-specific
methods are one service within the larger Quazal SDK.

| Service | Verified method range | Implemented | Remaining unsupported |
| --- | --- | --- | --- |
| Competition 26 | 1–4 | 1 (participant registration), 2 (owned round association) | 3, 4: codecs recovered; semantics still under audit |
| Title extension 101 | 1–39 | All 39; see title reference | None in this table; retail semantics still need validation |
| Matchmaking 21 | 1–36 | 1, 2, 4–16, 18, 20–24, 26–31 | 3, 17, 19, 25, 32–36 |
| Account management 25 | 1–19 | 1, 12, 13, 17 | 2–11, 14–16, 18, 19 |
| Authentication 10 | 1–5 | 1, 3 for the current login/service-ticket flow | 2, 4, 5; peer-service ticket destinations also need research |
| Secure connections 11 | 1–3 | 1 register; 3 published host URL lookup | 2 connection-data record lookup |
| Matchmaking extension 50 | 1–4 | All four: end participation, participant/detail queries and grouped URLs | Wider branch coverage pending |
| NAT traversal 3 | 1–2 | 1 inbound probe initiation; 2 outbound instruction and correlated response | Adaptive native fallback implemented; retail validation pending |
| Legacy route 15 | Binding not yet verified | 1 retained as compatibility alias for Ranking 1 membership | Actual Competition binding is 26; do not infer a full service from this alias |

The replacement policies have narrower scope than a complete generic SDK.
For example, 11/3 currently resolves live published room hosts with private-room
access checks. Statistics ranking uses a replacement policy while original
scoring remains unknown. “Implemented” means a real handler, not completed
retail validation of every screen and branch.

## Known matchmaking gaps

Names are cross-checked with the embedded metadata and numeric request
dispatcher `0x0053a7c0`:

| Pair | Operation | Notes |
| --- | --- | --- |
| 21/3 | UnregisterGatherings | Batch removal; individual 21/2 is implemented |
| 21/17 | FindByType | Generic typed gathering search |
| 21/19 | FindByDescriptionRegex | Generic regular-expression description search |
| 21/25 | FindBySQLQuery | Generic SDK query; title-specific 101/2 search is implemented |
| 21/32 | GetStats | Gathering report retrieval; 21/31 report submission is implemented |
| 21/33 | DeleteGathering | SDK deletion lifecycle |
| 21/34 | GetPendingDeletions | Pending-deletion records |
| 21/35 | DeleteFromDeletions | Deletion-list cleanup |
| 21/36 | MigrateGatheringOwnership | Generic SDK migration unsupported; stock TTLSession host handoff uses 21/27, see [host migration](host-migration.md) |

## Other SDK families visible in the build

Metadata also includes generic Friends, Notification,
distributed session/object protocols, master promotion/election and voice.
Their presence does not make them public backend endpoints. Some execute
between game peers. Do not assign server-side success stubs or infer protocol
IDs from string position. Numeric IDs, direction, layouts and authority must
be recovered before adding handlers.

The title's friend/clan UI uses implemented protocol-101 operations. Generic
Friends metadata separately names AddFriend, AddFriendByName, AcceptFriendship,
DeclineFriendship, BlackList, BlackListByName, ClearRelationship, UpdateDetails,
GetList and GetDetailedList; their backend use is not established here.

## Latest observed gaps and fixes

- Signup used 25/12 after successful creation/profile persistence. Email update
  was added and a subsequent full retail signup passed.
- Quick Game received a room result and host URL, then called 3/1. Native
  introduction was added; the host acknowledged its server-initiated 3/2.
- Joining then called 11/3 with CID 0 and the host PID. The recovered layout is
  two u32 selectors → bool plus a list of StationURLs. This lookup is now
  implemented for published live room hosts. Native join and match start passed;
  11/3 now also supplies a direct-first relay candidate when enabled.
- Round creation additionally used Competition **26/2**, ranking ID and round
  ID → bool. It now associates an owned TTLRound with its declared Ranking 1.
  Protocol 26’s dispatcher is `0x00534f58`; constructor `0x00534610` binds 0x1a.
  Its four-method codec table must not be confused with the older protocol-15
  candidate route. 26/3 takes U → B + L<U>; 26/4 takes no input → L<S>.
- Room exit subsequently used 50/1. The complete four-method extension has
  now been recovered and implemented; see the connectivity reference.

Tests cover each added operation's wire shape, authority and malformed input,
alongside actual transport delivery. These checks cannot reveal an unexercised
branch's next RPC. Continue auditing complete service tables while validating
the retail path; do not treat one successful operation as proof of a full flow.

Evidence: `work/25tolife/recovery-ranking-layout.c` (21),
`account-method-map.c` (25 reply dispatcher `0x00532db8`),
`connection-data-wire.c` (10 dispatcher `0x0054e8c0`),
`secure11-complete.c` (11 dispatcher `0x00551498`, reply decoder `0x00551900`),
and `nat-methods.c` (3 dispatcher `0x005430e8`). These private exports use the
ELF hash in [analysis.md](analysis.md). Their filenames describe research
sessions and are not reliable protocol identifiers by themselves.

Competition 26's binding is `0x00534610`, request dispatcher `0x00534f58`,
and reply decoder `0x005352b8`. The observed round registration was two u32s
(Ranking 1 and the new TTLRound ID), returning bool. Methods 3 and 4 have
verified bool+L<u32> and L<String> outputs but still need semantic recovery.

Extension 50's dispatcher is `0x00540908`, reply decoder `0x00540e48`, and
grouped URL record codecs are `0x00541bb0`/`0x00541c58`. Local exports are
`matchmaking50-wire.c` and `gathering-urls.c`.

Related references: [title methods](social-and-statistics.md) and
[connectivity/relay](connectivity.md).
