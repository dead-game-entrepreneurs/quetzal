# Player rating, lobby ranks and MOTD

## Sources and attribution

[michal-kapala/25tl-rdv](https://github.com/michal-kapala/25tl-rdv/tree/6d10c7469ea12c1028a44e14fceb1fb27c88c5c3)
provided the research lead to the original publisher's
[statistics FAQ, archived December 23, 2007](https://web.archive.org/web/20071223122600/http://stats.25tolife.com/faq/).
Its personal-stat schema also helped identify the server-owned rank slot in
the 102-value reply, consistent with the stock executable's consumers.
Credit to michal-kapala and the contributors to that earlier restoration.

The reference repository's [LICENSE](https://github.com/michal-kapala/25tl-rdv/blob/6d10c7469ea12c1028a44e14fceb1fb27c88c5c3/LICENSE)
contains MIT terms **and Commons Clause v1.0**, including a restriction on
selling the software and related services. It is not plain MIT. Quetzal's Go
implementation is independently written from published protocol facts, the
original FAQ and local executable analysis. No source, SQL, binaries, assets
or license-restricted implementation from that project are incorporated.

## Rating calculation

Players start at 1600. Rating is separate from accumulated raw round counters.
Overall rating occupies wire index 0; leaderboard rank occupies index 15.
The latter is read into the local online interface by `0x00347be8` and copied
to the replicated lobby rank during player setup. Side-specific rating fields
use the same global rating. Existing counters and report receipts remain
unchanged; old records without a rating field receive the default 1600.
There is no speculative historical rating backfill from accumulated totals.

The round's MVP performance score is calculated from the 87-value personal
report:

```text
score = 2 * (kills - team kills) - deaths - suicides
        - law-enforcement NPC kills + apprehensions
        + 2 * tags + 5 * (raid captures + robbery escapes)
```

An apprehension already counts as a kill, so its additional award is one point,
for three total. Adding another three would count that reward twice.

For each opponent on the other team:

```text
expected = 1 / (1 + 10^((opponentRating - playerRating) / 400))
actual   = 1 for a higher MVP score, 0.5 for a tie, 0 for lower
delta    = (16 + eligiblePlayerCount) * sum(actual - expected)
```

**Source ambiguity:** the FAQ's introductory equation prints a product of
differences, while its worked example explicitly adds them. Quetzal follows
the worked example's sum. Its four players finish at 1600, 2005, 1415 and 1780,
using nearest-integer rounding. This choice is covered by regression tests.
Player rating is bounded below by zero and above by the signed 32-bit range
used for lobby values. Calculations use one frozen set of prior ratings for
the whole round; player update order cannot change an opponent's baseline.

Only a TTLRound associated with Ranking 1 by `26/2`, whose host staging room
had Ranked Match enabled, qualifies. The authenticated host supplies the
expected roster. Each player's authenticated, nonempty personal report supplies
their own performance; participant aggregate copies cannot rate other players.
The server settles once all expected personal reports arrive or are explicitly
empty. A two-minute deadline settles the valid reporters if others never
report. Missing, empty or invalid performance rows are excluded, not invented.
A round without the authoritative roster cannot change ratings.

Pending rows and completion markers persist atomically with the statistics.
Duplicate reports, reloads and expiry cannot rate a completed round twice.
Unranked/staging reports still follow existing statistics handling but do not
affect skill rating. Clan skill starts at 1600, gains one point for a member's
rating increase, loses one for a decrease, and cannot fall below zero. Clan
membership is captured when rating collection begins; later joins do not
receive earlier members' changes.

## Lobby icons

The FAQ documents these native display rules:

| Standing / rating | Lobby icon |
| --- | --- |
| Leaderboard ranks 1–10 | Corresponding number; takes priority over stars |
| Below 1600, outside top ten | No rating star |
| 1600–1699 | One star |
| 1700–1799 | Two stars |
| 1800–1999 | Three stars |
| 2000–2199 | Four stars |
| 2200 and above | Five stars |

Equal ratings share the competition rank (one plus the number of higher-rated
players); PID gives tied rows stable display order. Top, ladder and explicit
player queries use the same rating and rank fields. Debug logging records
returned rank/rating and expected number/star selection. Rendering still needs
verification in the unmodified PS2 game; no client patch or custom icon is used.
The FAQ also identifies leaderboard gun/badge symbols as the side with more
Criminal/Law Enforcement wins. These are distinct from skill stars and numbers.

## Message of the day

`101/38` accepts an SDK locale string and returns an SDK message string.
By default the message is `Welcome to Quetzal. Have fun!`. To customize it:

1. Copy `configs/25tolife.motd.json` to `data/resources/25tolife.motd.json`.
2. Set `QUETZAL_TTL_MOTD_FILE=/data/resources/25tolife.motd.json` for Compose
   (use the real local path when running the binary directly).
3. Restart once to select that file. Later file edits are read on the next
   MOTD request, so announcements can change without disconnecting players.

The JSON maps locales to messages. Names are case-insensitive; standard tags
such as `en-US` and `fr-FR` match English and French. Unknown locales fall back
to `default`, then English, then the built-in message. Messages are bounded to
1024 UTF-8 bytes, files to 16 KiB and locale count to 32. Duplicate normalized
locales, malformed JSON, NULs and unsupported control characters are rejected.
Bad startup files fail configuration; a bad live edit logs a warning and keeps
the last valid messages. Client reconnect/refresh is needed to request a new
message already cached by the game.
