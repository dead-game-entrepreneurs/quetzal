# Rainbow Six Vegas (PSP) — analysis notes

Static findings from the US UMD executable, from raw scans and a Ghidra 12.2
project (`ghidra/rsvpsp`, Allegrex processor). The `rsvpsp` adapter covers
account creation and login, confirmed in PPSSPP on 2026-11-22, plus empty
Spark list replies; see the [test guide](testing.md). Addresses are runtime addresses in
`BOOT.BIN`, which is unencrypted.

## Target build

| Item | Value |
| --- | --- |
| Game ID | `ULUS-10206` (`UMD_DATA.BIN`: `ULUS-10206\|0CFE2A58EF945014\|0001\|G`) |
| Disc version / required system | 1.02 / 2.82 (`PARAM.SFO`) |
| Executable | `PSP_GAME/SYSDIR/BOOT.BIN`, ELF32 MIPS (Allegrex), `EXEC` |
| Size / SHA-256 | 8,228,436 bytes / `725ad2b6ad8bbf97bd3322457da918a1d6badca63f0a3b6ed259f674c08dd49d` |
| Load | single PT_LOAD, file offset `0x1018` → `0x08804018`; `.text` `0x08804040`–`0x08c95814` |
| Entry | `0x08804128` |

The disc also carries an update (`PSP_GAME/SYSDIR/UPDATE`), a system-software
updater rather than a game patch.

## Endpoints and access keys

`0x0892f4bc(settings, region)` copies host and key with a 0x40 limit and
stores the port at `settings+0x2c0a0`:

| Region argument | Host | Key candidate | Port | Byte-sum mod 256 |
| --- | --- | --- | --- | --- |
| 1 | `rsvegaspspeu.quazal.net` | `oc9NeH4p` | UDP 30390 (`0x76b6`) | `0xaa` |
| other | `rsvegaspsp.quazal.net` | `R6V3g4s` | UDP 30380 (`0x76ac`) | `0x1f` |

`0x0892f390` fills the defaults: US first, then EU when `0x08804a60` returns
0 (the region check). It also stores the SDK guest password `h7fyctiuucf`
at `+0x2c05d`. `0x08937080` resolves the host with `sceNetResolver`, then
builds the Spark framework (`0x08b8a510` → `0x08b90988`) with the IP string,
port, access key and guest password.

## Transport (confirmed statically)

| Setting | Value | Evidence |
| --- | --- | --- |
| Access key | `R6V3g4s` (US) / `oc9NeH4p` (EU) | Framework config `+0xc` → `0x08bc320c` → `0x08bc8294` strncpy to `0x08f6ad44` (limit `0x7f`) |
| Checksum seed | **`0x1f`** (US) / `0xaa` (EU) | Stream setup `0x08bc7e44` passes the key to `0x08c1f820`, which byte-sums it |
| Payload wrapper | Plain raw/LZO selector byte, as WCC | Core constructor `0x08c19918` installs the compressor (`0x08c3c74c`, LZO 2.x init `0x2020`) when `0x08e0118c` is set; it is 1 in `.data` and nothing writes it |
| Transport RC4 | None | No `CD&ML` string and no RC4 object on the core; the only RC4 users are Kerberos ticket crypto (`0x08bce7c0`, `0x08bce9a8`) |

The formatted-key variant (`%s%d` with `0x08f6adc4`) is dead: its setter
`0x08bc3234` is referenced only from `.pdr` debug data. The `MD5Checksum`
plugin is HMAC-MD5 for tickets, not the packet checksum. The Quazal build is
Rendez-Vous "Version 2006", core dated Oct 17 2005.

So the WCC transport settings (seed from the key, `PlainPayload`) should
carry over, with ports 30380 (authentication) and a server-chosen secure
port.

## SDK and platform

- Quazal client sources: AuthenticationClient, TicketManager (login, acquire
  ticket), SecureEndPoint/SecureStream, KerberosAuthentication/Encryption,
  MatchMakingClient, NATTraversalEngine/RelayProtocol, NATEchoStream,
  PRUDPEndPoint/Stream, RC4Encryption, TransportSignatureGenerator.
- URL templates `prudp:/…` and `prudps:/…;PID=%d;CID=1;type=%d`.
- No embedded DDL method metadata (unlike World Championship Cards), but
  DDL class names remain (Gathering, GameSession, DynamicGathering,
  PrivateData/PublicData, Competition, Ranking, Tournament, and Spark's
  `AccountInfoPrivateData`/`AccountInfoPublicData`).
- No `sceNp*` imports: no PSN sign-in. Online uses `sceNetInet`,
  `sceNetResolver`, `sceNetApctl`; ad hoc play (`sceNetAdhoc*`) is separate.
- Ubisoft "Spark" layer (`Spark.cpp`, `SparkGameDDL.cpp`,
  `SparkIntegration.cpp`) drives the online UI.

## Protocol IDs

`0x08c32d18` stores a protocol ID byte at `+0x18` after the base constructor
`0x08c32b98`. Each client protocol class was tied to its request stubs
through the destructor in its vtable.

| ID | Service | Constructor | Request stubs |
| --- | --- | --- | --- |
| 1 | unknown; one method `(String)`, implemented by the client (vtable has a request dispatcher) | `0x08bce59c` | `0x08bd61f8` |
| 3 | NAT traversal | `0x08bd9624` | `0x08bd8ab4`… |
| 10 | Authentication (ticket granting) | `0x08bcbc38` | `0x08bd0084`… |
| 11 | Secure connection | `0x08bccc24` | `0x08bd36cc`… |
| 14 | Notification | `0x08bc3af4` | `0x08bcb308` |
| 20 | Friends (older 10-method form) | `0x08bd9a3c` | `0x08bdae94`… |
| 21 + 50 | MatchMaking (41 methods) + extension (4) | `0x08bec6f0` | `0x08bf4d58`…, `0x08bfdc28`… |
| 25 | Account management (25 methods) | `0x08bdd2b4` | `0x08be42b8`… |
| 26 | unknown, 4 methods (Competition DDL classes are present) | `0x08bea0a4` | `0x08beb35c`… |
| 60 | Ubisoft Spark title protocol (22 methods) | `0x08b915a8` | `0x08b9f884`… |

Every stub calls `0x08c356ac` (start request with the class's ID) and
`0x08c3308c(msg, method)`, then serializes arguments: `0x08c36d90` String,
`0x08c305ac(…, n, 1)` n-byte integer, `0x08c30b50` bool, `0x08bcb7f4`
ResultRange, `0x08c36f58` StationURL, `0x08ba4ee8` List<u32>,
`0x08bd9414` List<StationURL>, `0x08bc2cb8` seven Strings (Spark search
filter). `any<>` arguments go through the object factory at `0x08cf81ac` and
do not show up as a serializer call. The full extracted table is
`work/rsvpsp/out/requests.txt`.

A second, uncalled stub set with the same method numbers sits before most
classes (for example `0x08b9d6e0` for 60, `0x08bd2fe4` for 11). Its argument
lists differ in places, so it is ignored for now.

### Account management (25)

The request shapes match the NEX numbering: 1 CreateAccount (String name,
String key, u32 groups, String email), 4 ChangePassword, 11/12 update name
and email, 14/25 FindByNameRegex/Like (u32, String, ResultRange), 17
UpdateStatus, 21 CreateAccountWithCustomData (the four CreateAccount fields
plus two `any<>` objects), 23 UpdateAccount, 24 ChangePasswordByGuest
(String, String, String). Account creation runs as `guest` / `h7fyctiuucf`
(`JobCreateAccount`).

Spark's custom data:

- `AccountInfoPublicData` (`0x08b9c638`): String, bool, DateTime (u64), then
  four Strings. This matches the sign-up form (real name, gender,
  birthdate, country, …); field order still needs matching to the UI.
- `AccountInfoPrivateData` (`0x08b9bf58`): two Strings (likely secret
  question and answer).

### Spark title protocol (60)

Callers identify most methods; names are working labels.

| Method | Arguments | Called from |
| --- | --- | --- |
| 4 | bool, List<StationURL> | create game (`CreatingGameState`) |
| 5 | u32, bool, bool | game context `0x08b8b7bc` |
| 6, 7, 8, 17 | u32 / u32+range / u32,u32+range / u32+range | statistics and leaderboards (`StatsState`) |
| 9 | u32, List<u32>, ResultRange | statistics for a player list (`0x08b8c868`) |
| 10 | — | friends list (`FriendsState`) |
| 11 | u32 | player actions (add friend or invite) |
| 12, 15, 16 | u32,u32,+struct / u32,u32 / u32,u32 | report, close without stats, leave (`0x08b8b9e8`) |
| 13 | String, String | recover password (`RecoverPasswordState`) |
| 14 | String, String, String | validate secret answer (`ValidateSecretAnswerState`) |
| 18 | u32 | game debriefing |
| 19 | u32 | game context `0x08b8b014` |
| 20 | 7 Strings, ResultRange | browse matches with filters |
| 21 | bool, u32, u32, ResultRange | quick-match search (`SearchingGamesState`) |
| 22 | — | invitations list |

Methods 1–3 have no callers in this build. The response parsers (needed to
know what the server must return) are the `0x80xx` dispatchers, for 60 at
`0x08ba1f38`.

## Online features (Spark UI identifiers)

- Accounts: login, create (username, password, email, secret question and
  answer, real name, gender, birthdate, country), update info, recover and
  reset password via the secret answer, change name.
- Matches: create, browse, quick match, invitations, ranked, debriefing,
  play again.
- Social: friends, friendship requests, game invites (notification IDs
  GameInvite, FriendshipRequest, Accept/Decline variants).
- Statistics: own, friends, participants, leaderboards (ten stat fields).

## Account creation flow

`CreatingAccountState` (`0x08bac39c`…) logs in as `guest`, registers with
11/1 (the RegisterEx path at `0x08bccfc8` is taken only when the global holder
`0x08f6ad3c` carries data, which nothing sets), then `0x08bac48c` calls
`AccountManagementClient` `0x08bde0b0` with group mask 0x20. That job hashes
the password (`JobManageAccount::PasswordInitialHash`) and sends 25/21 from
`0x08be6544`.

Reply layouts, from the protocol 25 server-side stubs the client also links
(`0x08be6ea0`): 25/1 returns one qResult (`0x08be8fcc`); 25/21 returns
nothing (`0x08be9178` writes only the method ID). The 10/1 reply parser
`0x08bd0d8c` reads qResult, PID, Buffer, RVConnectionData (`0x08bd68a0`:
StationURL, List<u8>, StationURL) and a String, the shape the shared login
already writes.

The `any<>` writer `0x08bc9bcc` sends the class name, then a u32 length and
the object's serialized bytes, which begin with the Data record's own u32
body length (as in 25 to Life).

## Adapter (`internal/games/rsvpsp`)

- UDP 30380/30381, checksum seed `0x1f`, plain raw/LZO wrapper, WCC's
  retransmission budget, default account-name rule.
- Shared 25/21 (`CreateAccountWithCustomData`) handler, enabled only for
  titles with `ValidateAccountData`; it creates the account and both records
  in one store write and replies with no fields.
- `ValidateAccountData` checks the two record types and field layouts above.
- Spark list queries get empty lists (below); every other title call is
  refused and logged with up to 256 argument bytes.

## First in-game test (PPSSPP, 2026-11-22)

Guest login, registration, 25/21 and the named login all completed, creating
PID 1000. The menus then called 60/7 (u32 1, ResultRange 0/20) and 60/10
(no arguments), which were refused at the time.

Spark reply layouts from the server-side stubs at `0x08ba1f38`:

| Method | Reply |
| --- | --- |
| 6, 7, 8, 9, 17 | List of stat records (`0x08ba6b38`: u32, String, u32, then two float vectors, each a u32 count and the values) |
| 10 | List of friends (`0x08bc26b8`: u32, String, String, bool, bool, bool, u32, String) |
| 18 | u32 |
| 20, 21 | List<any<Gathering>> (`0x08ba47c4`), then a list of `0x08bf0e10` records |
| 22 | List of `0x08bc28a8` records, then a list of `0x08bf0e10` records |
| 4 | u32 (after the create-game call) |

The adapter answers 6–10, 17 and 22 with empty lists.

### Hosted games (60/4, 5, 19, 20, 21)

The first create attempt (2026-11-22) sent 60/4 with `any<SparkGame>` (a
90-byte body: Gathering base with zero ID/owner/host, min/max 0, policy 0x63,
flags 0x10, the match name, then the SparkGame fields from `0x08b9ce68`:
bool, twelve u32, bool), a bool (0) and two StationURLs for the host's game
socket, `udp:/address=…;port=1000;RVCID=1` and the same with the keys
reordered. Refusing it showed in-game error 00004021.

| Method | Request | Reply (server stub) |
| --- | --- | --- |
| 4 create | any<SparkGame>, bool, List<StationURL> | u32 game ID |
| 5 join (`0x08b8b7bc`) | u32 game ID, bool, bool | List<String> host URLs, List<stat record> (the players), any<Gathering> (`0x08ba4924`) |
| 9 player records (`0x08b8c868`) | u32 game ID, List<u32> PIDs, ResultRange | List<stat record> |
| 19 in play (`0x08b8b014`, host flag at context+0x68; from `0x0893207c` on entering page 0xfb6) | u32 game ID | none |
| 18 next host (`0x08bb8878`, debriefing) | u32 game ID | u32 PID hosting the next round; `0x08bb8a20` re-creates the match if it is the caller's |
| 20 browse | seven filter Strings, ResultRange | List<any<Gathering>>, List<GatheringURLs> (`0x08bf0e10`: u32 ID, List<StationURL>) |
| 21 quick match | bool, u32, u32, ResultRange | same as 20 |

The adapter keeps games in memory, one per host, stamped with the server's
ID and the host's PID as owner and host. The host's 60/19 is sent on
entering the game page (the Unreal lobby): at the first launch and again on
every return to the lobby after a round set. Games stay listed and joinable
while in play, since the game offers joining in progress ("Join in progress",
`T_MsgBoxText_JoinServerGame`). 60/16 (game ID, play time; sent by
`0x08b8b9e8` when a context closes without a report) is a player leaving: the
adapter removes the player and tells the others (900000), or closes the game
if the host left. Every 60/16 in the logs preceded a new game or the menus;
before this, a player who left after a match stayed listed, so rejoining
announced nothing to the host. A game also ends on a new create by the same
host or the host's disconnect. Browse and quick match
list every other player's game, paged by the ResultRange (at most 100);
filters and the meaning of the SparkGame fields are not decoded yet. Join
returns the host's URLs as sent and an empty stat list.

### First LAN match (2026-11-22)

Two PCs (PPSSPP) created accounts, one hosted, the other found the match by
browsing, joined and played it to the end. Around the match:

| Call | When | Request | Reply (stub) | Adapter |
| --- | --- | --- | --- | --- |
| 3/3 | joiner, before joining | List<StationURL> targets (the host's game socket), StationURL self | void (`0x8003`) | acknowledged; no probes sent yet |
| 21/39 | joiner, after 60/5 | u32 game ID, List<StationURL> (own game socket) | void (`0x8027`) | acknowledged and logged |
| 60/16 | a player leaving (first seen when a host cancelled its new game) | u32 game ID, u32 play time (seen 1, 0) | void (`0x8010`); 60/15 the same shape | 16 removes the player; 15 is logged |
| 60/12 | every player, at the end | u32 game ID, u32, List<stat record> (`0x08ba6bc0`: u32 PID, String name, u32, two polymorphic objects) | void (`0x800c`) | acknowledged and logged |

Refusing 60/12 left both games on "Waiting for server action".
Acknowledging it was not enough: the joiner then showed "Waiting for server
action" (`T_WaitingClient`, waiting for the host PSP) and the host "Waiting
for other players" (`T_WaitingOnline`), with no further calls. The host's
report carried one record (its PID, name, u32 2, two empty nested objects);
the joiner's carried none. The second value (0x130 and 0x12e) differs per
player.

### Player lists and notifications

Each PSP keeps the match's players in its game context (`+0x8c`), and only
server notifications change it. The 14/1 handler `0x08b8c340` routes on
type/1000:

| Type | Fields | Effect |
| --- | --- | --- |
| 900000 + status | param1 player PID, param2 game ID, String name | add or update that player with the status; status 0 removes it (`0x08b8c3d8`) |
| 901000 | param1 game ID | refresh the players' records with 60/9 (`0x08b8c868`) |
| 4000 | param1 game ID, param2 new host PID | this PSP becomes host if param2 is its PID (`0x08b8c9b0`) |

The host adds itself with status 2 when creating (`0x08b8b354`). Without the
900/901 events the host never learned of the joiner, which is the likely cause
of the end-of-match stall. The adapter now keeps each game's players, returns
their records in the 60/5 reply and from 60/9, sends 900002 and 901000 to the
other players when someone joins, and 900000 when a player's registration
ends. Records carry PID, name, status 2 and two empty stat objects.

With the notifications in place (2026-11-22, second LAN match) the host
called 60/9, and both 60/12 reports listed both players with real stat
objects (for example a u32 8 followed by float values such as 3.0, 4.0, 10.0
and 1.0, 14.0). The host then called 60/18, whose refusal showed "operation
error" on the host; the adapter now answers it with the current host's PID.

Third LAN match: after 60/18 the host re-created the match for the next
round (60/4, new game ID, only `udp:/fullhouse` as its URL) and continued,
but the joiner made no further calls and dropped out about 40 seconds later.
The host's new game context lists only itself, so the adapter now carries the
previous game's players into a `udp:/fullhouse` game and sends 900002 and
901000 for it, on the assumption that the host admits only listed players.
This is unconfirmed.

Later matches stalled again on "Waiting for other players" without any 60/18.
PPSSPP's debugger showed the host's check `0x089362fc` returning 1: its
end-of-match event 0x7eb was posted but not handled. The results screen
handles it only when condition `0x08bb86d4` holds, i.e. when no completed,
unconsumed player-records result is pending (`0x08b8bed4`); `0x08bb81ac`
consumes a 60/9 result that completes while the results screen is up. The
adapter therefore sends 901000 to the game's players after every 60/12, so
they fetch 60/9 again, and 60/9 now returns each player's latest reported
record (PID, name, status, then two objects of a u32 count and 4-byte values;
the first held eight floats; see Statistics below).

Across all matches on 2026-11-22 the host reached 60/18 exactly when the game
ID was 1 (five matches) and never with IDs 3 or 4 (four matches), regardless
of connection IDs or restarts. The cause turned out to be the adapter: 60/9's
first argument is the constant stat set 1 (`0x08b91258`), which the adapter
read as a game ID, so in any other game 60/9 answered with no records and the
results screen kept waiting. 60/9 now looks up the caller's game. The adapter
also gives new games the lowest free ID, and keeps the
previous ID for the host's next-round re-create (`udp:/fullhouse`), so that
the other players' contexts, which still hold that ID, accept the
notifications for the next round.

With those changes (17:18) the host reached 60/18 in 10 seconds and the
next round kept game 1 with both players. The joiner then called 60/18
itself (answered with the host), and searched for the host's new game with
MatchMaking 21/18 FindByDescription (String, ResultRange; reply
List<any<Gathering>>, stub 0x8012). The host names the next-round game
`(%d)%s` (`0x08bb896c`): the previous game ID and the host's name, e.g.
`(1)TRIS`. Refusing 21/18 showed "Failed to create/join game session". The
adapter now answers it by exact description, and a next-round game's join
reply carries the previous round's real host URLs instead of
`udp:/fullhouse`.

Confirmed at 17:33–17:46: two PCs played three rounds of one match. Each
round the host re-created game 1 with `udp:/fullhouse`, the joiner found
`(1)TRIS` with 21/18 and rejoined, and after the last round both players
returned to the lobby. The server refused no calls in that session.

### Statistics (60/6, 7, 8, 12, 17)

Every statistics call starts with u32 1 (a stat set; `0x08b91130`…`0x08b912c8`
pass the constant). StatsState (`0x08bc11b4`) cycles through four modes with
"Next" (`0x08bc1054`; mode 3 only while in a game, `0x08bc100c`). The button
each mode offers (`0x08bc1480`, UIActionID 0x7ef FriendsStats, 0x7f0
ParticipantsStats, 0x7f1 OwnStats, 0x7f2 LeaderBoardStats) names the next
mode, which identifies them:

| Mode | Page | Call | View |
| --- | --- | --- | --- |
| 1 | 0xfc0 | 60/7 (1, ResultRange 0/20) | own; the success handler `0x08bc10cc` selects the caller's row |
| 2 | 0xfc1 | 60/6 (1) | friends |
| 3 | 0xfc2 | 60/8 (1, game ID, ResultRange) | participants |
| 4 | 0xfc3 | 60/17 (1, ResultRange) | leaderboard |

All reply List<stat record>. The table helper (`0x08bc1c1c`, `0x08bc1f2c`;
in state 0x22, StatsState) builds cells name, rank (first vector `[3]`), rating
(`[0]`), N stat cells (`[5]`…), play time ("%d min" from the second vector `[0]`).
The column names come from the debug table `0x08ba7970` (0x1b61 Rank, 0x1b5a
Rating, 0x1b64 Play Time, 0x405+ "Stat %d" up to ten). The page filler
`0x08935600` shows Rank (cell 1), Name (cell 0, highlighted if the player's),
Rating (cell 2), Accuracy (cell 4 / cell 5 × 100) and Play Time (cell 6), so
N is 3 and `[6]` / `[7]` are hits / shots. Elsewhere (the results screen) `[0]`
shows with its change `[2]` and `[3]` with `[4]`.

Reports (60/12: game ID, u32, List<stat record>) carry the same eight values
per player, the same table from every player: `[0]` the rounds the player won
in the round set (2 and 1 after three rounds; Team Survival too), `[5]` 0 so
far, `[6]` hits, `[7]` shots, an empty second vector. The u32 is the play time in
seconds (180–184 for about three minutes). The adapter keeps careers in
`stats.json`: it counts each player's first report per round set, adds rounds
won, hits, shots and play time, and moves an Elo-style rating (start 1000, K
32) on rounds won against every other player of the round. It serves rating,
its last change and rank in the first vector, play time in the second.
Results are marked counted only after the stats file is saved; a failed save
returns an RPC error and leaves another client's upload eligible to retry.
Players carried into a `udp:/fullhouse` game must join the new round with
60/5 before their reports are accepted. This keeps a late previous-round
report from consuming the new round's stats, while allowing identical
results in successive rounds.

### How the results screen moves on

GameDebriefingState (`0x08bb8368`) registers three events. 0x7eb (handler
`0x08bb8878`, 60/18) runs only when condition `0x08bb86d4` holds (game
context flag `+0x2c` set and no unconsumed 60/9 result, `0x08b8bed4`). 0x7ee
(`0x08bb8c90`) leaves the game context, and 2000 selects a player. The events
are not driven by the server or by input on that screen: the Unreal-side
travel handler `0x088a4734` posts them when the host moves its players on.
Travel to "Lobby" (after the last round) calls `0x089362fc(…, 0)`; the
in-match path at `0x08928038` calls `0x089362fc(…, 1)` for the next round.
`0x089362fc` posts 0x7eb only from state 4 on page 0xfb7 and returns 0
(posted), 1 (a 0x7eb is already waiting) or -1 (wrong state or page).
Travel to "Menu" posts 0x7ee through `0x08936680` and returns to the online
start menu.

In one Team Survival match (2026-11-22 18:26) the joiner never called 60/18
after the last round although the server's traffic matched the working runs;
the host returned to the lobby alone (60/19 32 s later) and the joiner
disconnected 7 s after that. It happened again at 20:28 with the roles
swapped, again after the last round set of a Team Survival match: the
joiner's only call after the host's re-create was the 60/9 that the
re-create's 901000 asked for (completed 65 ms later), then nothing.

A 60/9 reply is handed to the registered listeners on completion
(`0x08b8c2b0` → `0x08b8c2dc`); the results screen's listener `0x08bb81ac`
consumes it only if that screen is the current state, and an unconsumed reply
holds back 0x7eb (`0x08b8bed4`, `0x08bb86d4`). The re-create's 901000 puts a
joiner's 60/9 near that moment, but leaving it out (21:07 session) did not
help, so it was put back.

SparkGame settings (decoded from the LZO-compressed 60/4 captures): of the
twelve u32 fields, the seventh is the mode (1 Survival, 2 Team Survival) and
the eighth the map (`T_Map1`…`T_Map6`). Reports are the same in both modes.
Last transitions so far:

| Session | Mode | Map | Round sets | Joiner after the last |
| --- | --- | --- | --- | --- |
| 17:33 | Survival | 2 Prison | 3 | 60/18 after 6 s |
| 18:15 | Team Survival | 4 Bank | 3 | none; dropped |
| 19:11 | Team Survival | 4 Bank | 3 | 60/18 late (paused in the debugger) |
| 19:33 | Team Survival | 2 Prison | 2 | 60/18 after 4.5 s |
| 19:58 | Survival | 4 Bank | 2 | 60/18 after 4 s |
| 20:24 | Team Survival | 4 Bank | 1 | none; dropped |
| 21:07 | Team Survival | 5 Import/Export | 1 | none; stayed online, silent |

Every failure was Team Survival, but Team Survival also worked once, and the
server's traffic is the same in all of them. The cause is on the joiner's
side and still open.

### Friends (20, 60/10, 60/11)

The first field call was 20/2 AddFriendByName from the friends page:
`0500545249530000000000010000` = String "TRIS", u32 details 0, String "".
Protocol 20 is the older ten-method Friends protocol. Request stubs
`0x08bdae94`…`0x08bdbe78`; server-side stubs in `0x08bdc03c`:

| Method | Arguments | Reply |
| --- | --- | --- |
| 1 AddFriend | u32 PID, u32 details, String message | bool |
| 2 AddFriendByName | String name, u32 details, String message | bool |
| 3 AcceptFriendship, 4 DeclineFriendship, 7 ClearRelationship | u32 PID | bool |
| 5 BlackList, 8 UpdateDetails | u32 PID, u32 details | bool |
| 6 BlackListByName | String name, u32 details | bool |
| 9 GetList | u8 relationship, bool reversed | List<u32> |
| 10 GetDetailedList | u8 relationship, bool reversed | List<FriendData> (u32, String, u8, u32, String; `0x08bdd1b4`) |

Friend notifications: NotificationManager `0x08bac9d8` handles any 14/1 type
with type/1000 == 1. param1 is the other player's PID, param2 the
relationship: 2 → UI 3005 FriendshipRequest, 1 → 3006 AcceptFriendship, 0 →
3007 DeclineFriendship ("Friendship declined or deleted"). UI IDs 3000–3007
follow the `UINotificationID` names in order (GameInvite, AcceptGameInvite,
DeclineGameInvite, CancelGameParticipation, CancelGameInvitation,
FriendshipRequest, AcceptFriendship, DeclineFriendship). Types 3002–3006 map
to the game-invitation events (3003 → GameInvite, 3004 → AcceptGameInvite,
3005 → DeclineGameInvite, 3006 → CancelGameInvitation, 3002 →
CancelGameParticipation), with param2 as the data.

60/10 (FriendsState `0x08bb74c4`, success `0x08bb75a0`) returns a list of
u32 PID, String name, String status, bool, bool, bool, u32, String
(`0x08bc26b8`). Status "Online" shows as online; the second and third bools
give the row an icon (flags 4 and 2, `0x08933fc4`) and hide the rating and
game; the u32 is a float rating (`lwc1`, shown `%-5.0f`); the last String is
the game, shown after its `)`. The adapter sends friend, incoming request,
outgoing request, which is the 60/11 order; which pending icon is which is
unconfirmed.

60/11 (PlayerActionsState `0x08bbd84c`) takes a PID and returns bool, bool,
bool, bool, u32, String, bool (`0x08bc2a50`). The menu actions (`0x08bbd77c`,
IDs 0x1394–0x139a in `UIMenuItemID` order) read them as:

| Field | Action |
| --- | --- |
| bool 0 friend | RequestFriendship (20/2 by name) when false |
| bool 1 incoming request | AcceptFriendship (20/3), DenyFriendship (20/4) |
| bool 2 outgoing request | DeleteFriend (20/7), also for a friend |
| bool 3 game invitation, u32 game | AcceptGameInvite (60/5 join), DeclineGameInvite (21/7 with "") |
| u32 game, bool 4 joinable | JoinGame (60/5) |

The String is not read there. The adapter keeps friendships and requests in
`friends.json`, answers 1–4, 7 and 8, and refuses blacklists and the plain
lists (no callers found). Asking someone who already asked accepts.

### Public-URL probe before hosting

Choosing to create a match first makes the game socket (PSP port 1000) send a
16-byte PRUDP USER packet to the secure port, every 250 ms for ten tries:
streams `71`/`71`, checksum seed 0, payload `34120000 02`. With no answer the
game closes its session. This is the same SDK public-URL probe as TDU's (see
`docs/games/tdu/nat-probe.md`), with the title adapter's marker 0x00001234
instead of TDU's 0x0000cd80. The game's receive loop `0x089543a8` (also
`0x08954178`) compares the first four bytes of each datagram with 0x1234 and
passes the rest to the Quazal transport, so the reply is a raw datagram:
`34120000 02` + `udp:/address=<observed IP>;port=<observed port>` + NUL. The
adapter sends it from the secure port only.

## Next steps

1. Test statistics in PPSSPP (all four views, and a match in a game other
   than game 1).
2. Game invitations (MatchMaking 21/5–8, events 3002–3006, the 60/22 list and
   the 60/11 invitation flag).
3. Play across NATs: 3/3 is acknowledged without sending probes.
4. Confirm the fix for the joiner drop after the last round set (see above).

Local helpers (ignored by Git): `work/rsvpsp/tools/` has the raw scanners
(`xref.py`, `dis.py`, `immscan.py`, …), `decompile.sh` (read-only queries
against a VM-local copy of the Ghidra project, refreshed with `sync.sh`), and
the stub extractors `req.py`/`fmt.py`, which read a full decompile
(`out/all_*.txt` concatenated). Strings are in `work/rsvpsp/strings.tsv`,
extracted disc files in `disc/rsvpsp/`.
