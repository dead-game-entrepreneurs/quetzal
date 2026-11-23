# Commandos Strike Force online service map

US PS2 `SLUS_211.03`, SYSTEM.CNF 1.02; research dated 2026-11-18.
Executable hashes and overlay provenance are in [analysis.md](analysis.md).
These are static findings and limited native codec checks. The resulting
[experimental adapter](running.md) has synthetic UDP coverage and a successful
two-PC LAN match-entry test. The run guide separates those retail observations
from remaining gameplay and connectivity checks.

## Main findings

Commandos is a conventional Rendez-Vous/Net-Z integration, with a substantially
smaller linked backend surface than 25 to Life. Reuse the Quazal authentication
and PRUDP foundations, and develop a separate gathering service. TDU's edNet
master, resource, presence and race services are not part of the recovered path.

Three important differences prevent enabling an existing adapter unchanged:

- Signup first logs in as the shipped identity `**CuentaCrearCSFv1**`, with the
  same text as its password. The shared service's default identity is `guest`;
  Commandos supplies a separate signup policy.
- Lobbies use `Commandos3DGameGathering`, with a different binary body and
  description-regex search `21/19`.
- Native gameplay installs LZSS compression and a title-specific RC4 key.
  That is separate from ticket encryption and from the backend packet stream.

Start with [authentication](authentication.md), [matchmaking](matchmaking.md)
and the [reuse/implementation plan](reuse-plan.md).

## Connection path

```text
PS2 network configuration
  -> DNAS overlay CYBOR_VF.BIN
  -> Rendez-Vous overlay RVOUS_VF.BIN
  -> configured authentication host cxliveSCEA.quazal.net:30060
  -> ticket-granting login, password-key validation and service ticket
  -> secure endpoint selected from returned connection data
  -> secure registration
  -> gathering browse/host/join and NAT introductions
  -> Net-Z peer session, replicated gameplay, chat and voice components
```

The CYBOR image contains SCE DNAS authentication and SSL strings; it is not a
second Quazal backend. It and Rendez-Vous occupy the same overlay address
`0x8c0480`. Keep them in separate analysis images. DNAS remains a separate
service. Do not route all of its SSL traffic into Quetzal.

The secure URL can be supplied by the authentication reply. The fallback at
`0x8c2ff0` uses the configured port plus an SDK service-PID-dependent offset;
do not mistake 30060 for the port of every subsequent service.

## Feature sweep

| Feature | Evidence in this PS2 build | Backend implication |
| --- | --- | --- |
| Named account login/logout | Game facade `0x461c80`, `0x461f60`; ticket/login jobs | Shared auth foundation, with title policy |
| Account creation | `0x465840` -> `0x463700` -> account client | Custom signup principal and parameter 0 through shared policy |
| Password change | `0x465a60` -> `0x463e60` -> 25/4 | Shared opt-in handler; bool reply |
| Existing-account recovery | UI offers reconnect/recover after duplicate creation | Reuse name lookup/login; verify result codes in game |
| Delete saved account/profile | Shared menu text exists | No outgoing account-delete writer found; distinguish local save deletion |
| Host/create lobby | `0x462aa0`, `0x464610`, 21/1 | Commandos gathering codec/registry |
| Browse/search/filter | `0x4621b0`, regex builder `0x456080`, 21/19 | New search handler; pagination is present |
| Quick match/OptiMatch | Shared lobby UI calls the same online facade | Discovery/filtering, not a separate TDU-style service |
| Join/leave | 21/11 and 21/12 writers plus game callers | Reuse lifecycle concepts from 25 to Life |
| Lobby updates/player count | 21/4; `0x4650c0`, `0x466780` | Preserve title fields and validate host authority |
| Player list and host URLs | 21/13, 21/16, extension 50/2 | Needed for peer discovery and ping/probe work |
| NAT traversal | Protocol 3; URLProbe and Peer classes used by game | Reuse shared probe machinery after direction/identity checks |
| Host migration | Net-Z extension; `0x6ec650` calls 21/27 | Existing TTL owner-only update policy needs review |
| Deathmatch/team deathmatch/sabotage | Game options and query helpers identify modes 0, 1, 3 | Match metadata and peer game rules |
| Eight-player configuration | `CONFIG/JUEGOVER.CFG` `.uMaxPlayersMP 8` | Retail configuration limit, not a proven server admission rule |
| Match statistics/scoreboard | NetPlayer DDL counters and `IncEstadisticas` | Peer replication/local display; no backend reporting path located |
| Persistent ranking/leaderboards | No ranking/statistics client family or outgoing writer found | Do not copy TTL rankings or TDU title statistics without evidence |
| Friends/friend requests/invites | Shared menu assets, but PS2 social facade slots are stubs | No active friends RPC family found; menus alone are misleading |
| Clans | No clan client or title extension found in the inspected online surface | No established requirement |
| Text chat | NetChat DDL with `MensajeChatEx`, `MensajeFliEx` | Net-Z peer feature, not an account messaging endpoint |
| Voice/mute controls | VoiceChannel, streaming, PS2Logitech and LPC10 classes; UI controls | Linked peer capability; microphone/retail behavior untested |
| Downloads/patches/challenges | No corresponding title-specific service clients found | No evidence for TTL patch or TDU resource services |

Linked SDK reply tables contain methods for which the linker retained no request
writer or the game has no identified caller. These do not establish a retail
feature. Similarly, shared resources include Xbox-specific strings and menus;
their presence does not establish PS2 Xbox Live/social support.

### Linked Rendez-Vous protocol families

| Protocol | Family | Scope in this build |
| --- | --- | --- |
| 1 | RemoteLogDevice | SDK server interface; not a player feature |
| 3 | NATTraversal | Requests and incoming peer-introduction work |
| 10 | TicketGranting | Account login and service tickets |
| 11 | SecureConnection | Registration and connection-data queries |
| 14 | Notification | Incoming SDK notifications, bound by `0x8c1b40` |
| 21, 50 | MatchMaking and extension | Gathering lifecycle, discovery and participant filtering |
| 22 | Peer | Client/server peer interfaces, bound by `0x8e2610` |
| 25 | AccountManagement | Signup, password changes and status |

This inventory combines retained RTTI with protocol bindings and request/reply
dispatchers. Net-Z object replication and streaming protocols in FIN_RED are a
separate peer layer; their dataset names do not imply extra backend RPC services.

### Statistics evidence

`FIN_RED.BIN` contains `NetPlayerDDL.cpp` at `0x7df1b0`, the method name
`IncEstadisticas` at `0x7df250`, and replicated datasets for enemy kills, team
kills, deaths to enemies, deaths to teammates, suicides and captured flags
(`0x7df390`–`0x7df470`). These belong to Net-Z player objects, alongside health,
weapons and respawning. A captured-flags field alone does not establish a
separate playable capture-the-flag mode.

No FriendsProtocolClient, RankingProtocolClient, statistics-reporting client,
clan client or title-specific protocol-101 equivalent was found in either
FIN_RED or the complete Rendez-Vous overlay inventory. Preserve that negative
finding as the scope of this sweep, rather than a universal claim about every
region/platform.

The successful two-PC LAN test on 2026-11-23 produced authentication, gathering
and NAT RPCs, with no backend statistics upload or ranking/friends/clan requests
and no backend error replies. The tester confirmed match entry, leave, Quick
Match re-entry and player-count changes. Round completion/reporting has not yet
been tested, so the absence of a post-round upload remains a static finding.

### Social evidence

The lobby's `BotonFriends` is handled by `0x30f800` and `0x30fdf0` through the
control's disabled/hidden path. The player-options friend control at
`0x390260` checks platform/social flags before calling a social facade.
The PS2 facade vtable at `0x13d800` maps its social-object accessor at `+0xfc`
to `0x4672a0`, which returns zero. Related slots `+0xf8`, `+0x100`, `+0x104`,
`+0x108` and `+0x10c` also return zero or do nothing. No supporting friends
protocol client is linked. This is stronger evidence than the shared menu names.

## Evidence and verification limits

The combined Ghidra image maps COMMX_FR, FIN_RED and RVOUS at their retail
addresses. Initial automatic analysis reached its 300-second limit; targeted
read-only decompilation/disassembly recovered additional functions afterward.
This is not exhaustive automatic analysis of every instruction.

Private exports include `rendezvous-all.c` (666 auto-discovered overlay
functions), `game-online.c`, `policy-and-ui.c`, `prudp-codecs.c`,
`final-network-sweep.c` and subsequent transport/stream setup queries under
`work/commandos/`. Their findings are summarized here; do not commit the exports.

`tools/verify_commandos_gathering.py` executes both gathering field codecs with
synthetic input and hooked stream primitives. Both produce/consume 559 primitive
operations and restore the complete synthetic object. This checks field layout,
not complete transport or server compatibility. The same tool checks 292 native
checksum cases against the shared arithmetic. Runtime coverage and current
limits are recorded separately in the [run guide](running.md).
