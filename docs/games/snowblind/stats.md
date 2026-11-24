# Snowblind statistics and leaderboards

## Evidence and current behavior

Audited 2026-11-23 against the US `BO3NETGM.ELF`, SHA-256
`00f19fd1c63f395ec6e3dfe1736c0a9c80e63becf4497d43a5fe095169ddc8c3`.
Addresses below are native virtual addresses. Ghidra exports and private retail
captures remain under ignored `work/snowblind/` and `data/captures/snowblind/`.
No original server implementation or ranking algorithm has been recovered.

The four failed retail menu requests were all **101/2 GetStats**. The local
adapter supplies native-shaped replies and now accepts/persists **21/31**
round summaries and **101/4** personal maps. Earned totals populate all four
menus. Accounts without earned records retain zero counters and rank -1;
unknown names return no rows. Both clients have now completed a retail
round-end upload with all 35 personal fields. Reload, duplicate replay on a
temporary copy and native leaderboard reply checks pass against that saved
round. The tester subsequently confirmed all four populated menus.

The first two-player retail round-end capture reached **21/31** from both
clients. Each sent the same complete two-player, category-1 report with five
floats per player (72 argument bytes). Kill/death and penalty slots were zero;
winner slots differed between the players. This confirms duplicate full-roster
reports across clients, so accumulation must award each round/player once.
The build used for that capture rejected the then-unimplemented method, and
**101/4** and **101/5** did not follow in the observed traffic. Personal-report flow still
needs validation after a successful summary acknowledgment. The private decoded
capture summary remains under `work/snowblind/round-report-summary.json`.

A later two-player relay setup test also exercised an ordinary early exit:
the client sent a category-2/default summary and an empty **101/4** map. Both
were acknowledged and persisted across the subsequent server restart. No earned
totals were invented. The later full retail round is recorded below.

## Menu requests and rows

Request writer `0x15e168` emits `u8 selector, String name, u32 maximumRows`.
The UI requests 15 rows; there is no explicit offset/range structure or SQL.

| Selector | UI operation | Native caller |
| --- | --- | --- |
| 0 | Player Search, supplied name | `0x209d80` |
| 1 | Top Players, empty name | `0x209cd8` |
| 2 | Friends statistics, empty name | `0x209e30` |
| 3 | Clan statistics, empty name | `0x209ed8` |

The response is `u32 rowCount`, then each row:

```text
String playerName
String clanAbbreviation
u32 principalID
u32 valueCount
float32 values[valueCount]
```

Native row writer/reader: `0x15fa70` / `0x15fb30`. Conversion to the UI wrapper
is `0x232a78`; `0x20a068` consumes the values as follows:

| Index | Meaning / use |
| --- | --- |
| 0 | Ignored by this menu; meaning remains unverified |
| 1 | Integer displayed rank; negative displays the unknown marker |
| 2 | Kill/death ratio numerator, interpreted as cumulative kills |
| 3 | Kill/death ratio denominator, interpreted as cumulative deaths |
| 4 | Displayed wins |
| 5 | Displayed losses |

The K/D interpretation follows the UI labels and native division of slots 2/3.
A nonpositive denominator produces native sentinel -9999, displayed as infinite.
The UI sets its accuracy member to -1 and this renderer never populates an
accuracy cell, despite retaining the accuracy heading/localization key. Shot
counts are nevertheless present in personal uploads and can support accuracy
outside this menu.

Six values suffice for this menu. The `0x41` passed when constructing its float
list is an allocator category, **not evidence of a 65-value wire schema**.
Do not copy 25 to Life's 102-value response into Snowblind.

`tools/verify_snowblind_stats.py` executes the native row writer and menu
arithmetic with synthetic records, hooked SDK I/O/string helpers and the shared
Unicorn EE translations. It checks field order, rank, K/D, W/L and the unranked
zero-death branch. The public synthetic fixture is
`internal/games/snowblind/testdata/stats-native.json`. This does not substitute
for retrying the menus in the retail emulator.

## How results reach the server

Gameplay counters originate in the clients' peer session. The backend receives
summaries during the round-leave job (`0x232188`), rather than a backend RPC for
each kill, shot or augmentation. The normal reporting branch at `0x10e5e0`
requires a queried roster with at least two entries; otherwise it constructs
the default leave job (`0x231be8`) with an empty category-2 report.
Solo testing therefore need not produce any earned counters.

The leave job performs these operations in order:

1. In the full reporting path, update the round's state to 6 through **21/30**.
2. Send gathering summaries through **21/31 ReportStats**.
3. Send the authenticated player's named counters through
   **101/4 AddStatsPersonal**.
4. Call **101/5 EndParticipation** for the round.

The default leave path skips step 1, has the category-2 empty summary and sends
an empty personal map. The backend must acknowledge an empty report without
inventing values or incrementing games played.

### Gathering summaries: 21/31

Writer `0x367168`, reached through `0x364818`, serializes:

```text
u32 roundGatheringID
u32 recordCount
repeat recordCount:
    u32 principalID
    u32 category
    u32 valueCount
    float32 values[valueCount]
```

The record codec is `0x36dbc0`. The full leave constructor `0x231dd8` uses
category **1** for each player and copies that player's five-value summary;
the default constructor uses category **2** for self with no values.
Its successful application reply is a bool (`0x3695e8`, case 0x801f).
Summary construction `0x1206b0` appends:

| Index | Native source | Interpretation |
| --- | --- | --- |
| 0 | First replicated scoreboard record +2, u16 converted to float | Kill counter |
| 1 | Same record +0, u16 converted to float | Death counter |
| 2 | `0x121578` result | Winner code: normally 0/1 for player/side comparison; can return 2 when the team-winner byte is 0xff |
| 3 | `0x1218a8` output at argument 4 | Score-based winning flag; same winner flag in team modes, compares against best score in modes 0/5 |
| 4 | Same scoreboard record +4, u16 converted to float | Penalty counter; exact event coverage still needs correlation |

The scoreboard accessor is `0x16ade0`. `0x1174f8` computes mode-specific scores
using kills minus the +4 penalty counter, with objective terms in some modes.
Preserve both winner values and the penalty counter independently until a controlled
completed-round capture confirms aggregation. Do not infer the original skill
rating or losses formula from these five values alone.

### Personal counters: 101/4

Writer `0x15e518` emits a named map, not TTL's positional vector:

```text
u32 entryCount
repeat entryCount:
    String fieldName
    float32 value
```

The successful reply has no application fields. There is no PID, round ID,
report ID or timestamp in this request. The authenticated connection supplies
the player identity; the backend must resolve the associated reporting round
from verified participation. The map's wire ordering is not a stat-index schema.

`0x10e5e0` builds **35 named entries**:

| Group | Exact field names |
| --- | --- |
| General, 6 | `timeAlive`, `numHitsTaken`, `numShotsFired`, `numShotsThatHit`, `numEnemiesKilled`, `numAlliesKilled` |
| Weapon kills, 11 | `killsNumCarbine`, `killsNumRocketLauncher`, `killsNumPistol`, `killsNumSniper`, `killsNumHerf`, `killsNumShotgun`, `killsNumFlechette`, `killsNumRailLaser`, `killsNumFragGrenade`, `killsNumDetpack`, `killsNumMelee` |
| Weapon deaths, 11 | `deathsNumCarbine`, `deathsNumRocketLauncher`, `deathsNumPistol`, `deathsNumSniper`, `deathsNumHerf`, `deathsNumShotgun`, `deathsNumFlechette`, `deathsNumRailLaser`, `deathsNumFragGrenade`, `deathsNumDetpack`, `deathsNumMelee` |
| Augmentations, 5 | `numUsesInvisibility`, `numUsesForcefield`, `numUsesSlowtime`, `numUsesEStorm`, `numUsesVision` |
| Map context/results, 2 | `mapId`, `mapWins` |

These are converted from the local counter block returned by `0x2999a0`, except
the current map ID and `mapWins` (from winner test `0x121578`). The time unit of
`timeAlive` remains unconfirmed. `mapId` is a dimension, never an additive stat;
accumulate `mapWins` by that map. Gas grenades have death-message strings but
no corresponding named kill/death field in this builder. No separate named
headshot, distance, damage or per-weapon accuracy fields occur in this upload.

## Persistence and reuse assessment

The server needs per-account named totals, per-map wins, separately stored round
summaries, and durable round/report receipts. Derived K/D and shot accuracy
should be calculated from totals; rank belongs to the server. Clan abbreviation
and Friends/Clan filters use actual accepted friendships and clan memberships.
Do not create social relations just because a stat query requests them.

The existing shared auth, bounded SDK codecs, account identities, Ranking
metadata store and transport already apply. TTL provides useful implementation
patterns for atomic persistence, round ownership, roster validation and duplicate
report handling. Its 87-value personal vector, 102-value browser row, clan
record and rating formula are title-specific and cannot be reused as this wire
contract. TDU title-data records likewise do not define Snowblind's schema.

## Upload persistence and deduplication

`stats.json` is private per tenant (0600). It retains server-assigned round
nonces, native gathering IDs, mode/map context, verified participant IDs, and
one receipt per authenticated reporter. Each receipt contains the full submitted
summary plus an independently acknowledged personal map. The same shared atomic
JSON writer used by Snowblind social state performs file fsync, rename and
directory fsync before success is published in memory or acknowledged.

Totals are derived from the receipts, including after restart. There is no
separately writable total that could diverge from the deduplication record:

- A report can reference only verified participants in that BO3Round. Summary
  uploads with category 1 require its BO3Stats competition association. Empty
  category-2/default reports are acknowledged even without that association.
- Only the authenticated reporter's own summary row awards that account. Other
  rows are retained for evidence and cannot award another player's counters.
- One nonce/player receipt contributes at most once. Reordered summary rows and
  identical retries are idempotent; conflicting replacements are rejected.
- Only a successful summary binds **101/4** to that connection's reporting round.
  Personal maps are acknowledged and retained once; duplicate keys, unknown
  fields, nonfinite/negative/oversized values and invalid dimensions are rejected.
  `mapId` must match the server's round map when that map is known. Counter
  values are bounded to 1e9; summary u16 counters to 65535.
- The account and exact secure connection generation must have participated.
  A slower member can report for up to 15 minutes after host cleanup. A reused
  gathering ID gets a fresh nonce; a participant's unfinished leave pipeline
  remains bound to the preceding round until EndParticipation. New server
  instances reject stale reports without live participation instead of treating
  reused wire IDs as the old round.
- Category-2/empty reports and one-row solo summaries award no games or counters.
  Category-1 multiplayer self rows contribute kills, deaths, penalties and one
  result: winner code 1 is a win, 0 a loss, 2 a draw. Score-winner slot 3 is
  preserved independently. This is the replacement aggregation policy.
- The personal weapon/enemy counters are retained independently and never added
  again to summary kills/deaths. `mapId` is a dimension; `mapWins` 1 contributes
  one map win and 2 one map draw. Other named fields accumulate as totals.

The receipt file retains all deduplication evidence. Current bounds are 8192
rounds and 32 MiB per tenant; reaching either limit rejects new writes rather
than pruning receipts and risking repeated awards. A scalable retention/archive
policy remains future work. Reports do not contain enough identity to recover
an interrupted round after a full server restart; unreceived reports for that
round are not backfilled from captures. Backend roster checks do not make
peer-reported gameplay counters authoritative against a modified client.

## Ranking evidence and menu behavior

Checked the [GameFAQs guide index](https://gamefaqs.gamespot.com/ps2/920210-project-snowblind/faqs)
and its three listed guides (two walkthroughs and the multiplayer class/weapon
FAQ). They did not provide a ranking formula. The
[official PS2 manual, printed page 24](https://support.na.square-enix.com/document/manual/1008/Project_Snowblind_PS2.pdf)
confirms K/D, wins, losses and an overall score derived from several statistics,
but does not publish the calculation. It also specifies a searched player among
nearby players, Top 15, best friends, and best clan members. The historical
`projectsnowblind.com/stats` endpoint is named there, but its formula has not
been recovered. No 25 to Life rating equation is applied to Snowblind.

The current explicitly **replacement** ordering is wins descending, kills
descending, deaths ascending, then PID ascending for deterministic ties. Only
earned multiplayer records receive an ordinal global rank. Slot 0 remains zero
because its historical score meaning/formula is unresolved; rank and counters
use the six native menu values. Friends and Clan filter the global ordering,
preserve global ranks, and honor the request's bounded row count. Player Search
centers the requested earned player in a window (normally seven above/seven
below, filling at the edges); an existing unranked player returns its baseline.

## Validation and remaining checks

Go tests cover two-client copies, repeated calls and row order, conflicts,
concurrent copies, restart/reload, wire-ID reuse, late reports/EndParticipation,
failed writes, corrupt state, full 35-field maps, social filters, and search
windows. The native StatsRow/menu verification harness still passes.

The completed two-player round and populated menu checks are recorded below;
saved-state reload and duplicate replay passed on an isolated copy. Further
retail checks should cover aborted rounds, draws, recording disabled, reopened
menus after a restart, and winner/penalty semantics. `timeAlive` units and the
original skill/overall-score calculation remain unconfirmed. Captures stay private.

## Completed relay round, 2026-11-24

Both clients completed **21/31 → 101/4 → 101/5** at 06:10:24–25 UTC
(00:10 Chicago). Each summary had two category-1 player rows, and each personal
map contained all **35** native named fields. There were no rejected calls in
this reporting sequence. The saved round contains exactly one receipt for each
reporter, alongside the earlier separate empty/default round receipt.

The host reported one kill, zero deaths and a win, with `killsNumCarbine=1`,
`numEnemiesKilled=1` and one map win. The connector reported zero kills, one
death and a loss, with `deathsNumCarbine=1`. Their summary values agree. Both
reported positive `timeAlive`; its units still need a controlled check. Shot
counters were zero in this test, so their relationship to hits is not established.

A separate Go verification loaded the actual private saved snapshot, replayed
its reports only into a temporary copy, and compared totals before/after. No
duplicate awards appeared. Actual native Top/Friends/Clan/Player query rows
also decoded correctly: the host is rank 1 with K/D 1/0 and W/L 1/0; the
connector is rank 2 with K/D 0/1 and W/L 0/1. Accepted-friend and clan filters
match the persisted social state. No account names or credentials were exported.

This confirms real upload acknowledgment, persistence, reload and deduplication
for the observed round. The populated in-game menus are now confirmed. Recording disabled, aborted
round semantics, ties, wider mode/map coverage and original ranking math remain
separate validation items. The live server was not restarted for this snapshot
check and its established adaptive relay stayed active.
