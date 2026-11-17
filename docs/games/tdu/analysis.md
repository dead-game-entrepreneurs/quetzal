# US PS2 protocol investigation

Research provenance: these findings were produced in the sibling `tdu-server`
workspace. Runtime package paths below refer to Quetzal; `tools/`, private
`work/` evidence and Ghidra projects refer to that original research workspace.
Keep those private files there; Quetzal has no runtime dependency on them.
See [running.md](running.md) for current Quetzal operation.

Initial static analysis and subsequent protocol deep dive: 2026-11-15.
Start with [the consolidated online map](online-map.md); this file retains the
build and initial endpoint inventory. No retail online session has been tested
against this project. Facts below come from the supplied disc and its Ghidra
analysis and limited native-routine execution. Current implementation policies
and retail-validation limits are called out in the linked service maps.

## Build and artifacts

| Item | Observation |
| --- | --- |
| Disc boot | `cdrom0:\SLUS_214.90;1` |
| SYSTEM.CNF version | `1.01`, NTSC |
| Main ELF size | 4,658,176 bytes |
| Main ELF SHA-256 | `620ea714b1dfd2d003a9791e5fabc2e8c9f1f6ac5c908f5873cd7e54972c7bf5` |
| Entry | `0x100008` |
| Main file-backed PT_LOAD | file `0x100`, VA `0x100000`, length `0x46f900` |
| GP clue | `_mips_gp0_value = 0x577170` reported by the loader |

Within that main segment, `VA = file offset + 0xfff00`. Use the ELF program
headers rather than applying this adjustment to every file or section.
The full selected-file hash inventory is [build.json](build.json).

The disc also contains `NETNTSC.ELF`, `OVERLAYS/MAINGAME.BIN`,
`OVERLAYS/DNASSCEE.BIN`, and SCE networking modules including `DNAS300.IMG`.
Selected research files also include `QNET.IRX`, `TWOFISH.IRX` and `TWOFISHT.IRX`.
Their presence establishes dependencies to investigate; it does not identify the
account-ticket cipher by itself. `NETNTSC.ELF` contains network configuration UI
text; its name alone is not evidence that it implements the online game server.

The main ELF's zero-size `MainGame.bin` and `DNASSCEE.bin` section descriptors
both name VA `0xaab080`. Import reports skipping those empty descriptors and
some invalid zero-size relocation offsets. The base main program analysis does not include that overlay. The subsequent
gameplay investigation verified the `MWo3` header and mapped the complete
MainGame file, including its header, at `0xaab080` in a temporary research
session. Native crypto call targets align with its function prologues, and its
end equals `0xbf1e00`, matching the main ELF heap marker. See
[the gameplay map](gameplay.md). DNASSCEE is a distinct overlapping image,
not the gameplay crypto implementation.

## Middleware and endpoint evidence

| Evidence | File offset | Main ELF VA |
| --- | --- | --- |
| `Quazal Rendez-Vous` | `0x3e9570` | `0x4e9470` |
| `Quazal::TicketGrantingProtocolClient` | `0x3e9040` | `0x4e8f40` |
| `PRUDPStream.cpp` | `0x3eb570` | `0x4eb470` |
| Authentication `prudp:/address=...` format | `0x3e82f0` | `0x4e81f0` |
| Secure-service `prudps:/address=...` format | `0x3e84a0` | `0x4e83a0` |
| `tdulive.quazal.net`, first copy | `0x41b3fe` | `0x51b2fe` |
| `tdulive.quazal.net`, second copy | `0x41b713` | `0x51b613` |

Both `0x1e1060` and `0x1e2e70` pass one of those hostname strings and `0x75f8`
(decimal **30200**) into Quazal account/login setup calls. This is static
endpoint configuration, not a captured connection. It does not establish that
all backend services use the same port.

`0x1e9070` constructs an authentication URL using the format at `0x4e81f0`:
`prudp:/address=%s;port=%d;stream=%d;sid=%d;type=%d`.
The stream, session ID and type arguments in this stage are `3`, `1` and `2`.
It then calls the endpoint connection routine `0x217290`.

After successful authentication processing, `0x1e95d0` selects a secure-service
URL. It can use an existing URL from connection data, or construct a fallback
using the `prudps:/` format. The fallback adds an offset of zero, one or two to
the original port depending on an SDK state value. Recover returned connection
data and confirm the branch in a retail trace before assuming a fixed secure
server port.

## Function map

These are main ELF virtual addresses. Stage names are supported by callback
metadata and cross-references, rather than original ELF symbols.

| VA | Recovered role / reason to inspect |
| --- | --- |
| `0x1e1060` | Account-creation setup referencing production host and port 30200 |
| `0x1e2e70` | Login path referencing production host and port 30200 |
| `0x1e7990` | Login setup called from `0x1e2e70` |
| `0x23ca50` | Account-creation setup called from `0x1e1060` |
| `0x23d550` | Account-creation job construction |
| `0x1e8fa0` | Schedules `ConnectToAuthenticationService` after argument checks |
| `0x1e9070` | Authentication PRUDP URL construction and endpoint connect |
| `0x1e9280` | Processes auth endpoint connection and schedules `Authenticate` |
| `0x1e9370` | `Authenticate` stage; calls `0x1f47d0` |
| `0x1e9500` | Processes authentication and schedules secure-service connection |
| `0x1e95d0` | `ConnectToSecureConnectionService` stage |
| `0x1f2f30` | Constructs ticket-manager login job |
| `0x1f3190` | `InitiateLogin`; selects `0x1f0e10` or `0x1f1050` |
| `0x1f0e10` | First ticket-login request path; passes method selector 1 |
| `0x1f1050` | Alternate ticket-login request path |
| `0x1f33a0` | Processes login result before scheduling key derivation |
| `0x1f3490` | Key-derivation stage, then schedules validation |
| `0x1f35d0` | Key-validation stage |

`refs:JobBackEndServicesLogin::ConnectToAuthenticationService` initially returns
`0x1e8fa0`, which schedules the actual stage through callback pointer
`0x4e8198` → `0x1e9070`. Likewise, `0x4e8300` → `0x1e95d0`,
`0x4e9000` → `0x1f3190` and `0x4e90b8` → `0x1f3490`.
Do not label every referencing function as the stage named in its string.

## Likely service scope and open questions

Linked class strings include ticket granting, secure connections, friends,
account management, persistent store, NAT traversal, messaging,
`ChallengeStoreProtocolClient`, `EventStatsProtocolClient` and
`LairAccountManagementProtocolClient`. They suggest substantial backend scope;
they do not prove which services the retail client requires for each menu.

The subsequent deep dive recovered the PRUDP prefix/extensions/checksum, core
connection flow, Quazal RPC envelopes, account and ticket derivation/container
formats, native bootstrap/tunnel path, FreeRide discovery/session logic and peer
action/crypto layers. Those results supersede the initial open-question list:

- [Transport and offline checks](transport.md).
- [Authentication, service tickets and signup](authentication.md).
- [Native service directory, configuration, groups and map presence](world-and-sessions.md).
- [Native tunnel, overlay crypto and peer gameplay](gameplay.md).

Some fields and server policies still need recovery, and none of these static
findings establish a successful stock-client login or race. The consolidated
map and roadmap identify the remaining implementation and retail validation.

## Local evidence and reproduction

Everything below is ignored/private under `work/ps2/`:

- `import.log`: initial Ghidra import and first endpoint/callback queries.
- `endpoints.log`: standalone `Research.java` queries of endpoint setup.
- `login.log`: login job and authentication connection stage queries.
- `auth-pass.log`: authentication calls and secure-service URL construction.
- `inventory.json`, `*.strings.tsv`: regenerated disc inventory and addresses.
- `tdu.gpr` / `tdu.rep`: analyzed program, processor `r5900:LE:32:default`.

The original sibling workspace retains `tools/research.py` and the Ghidra
scripts to regenerate these artifacts from your own disc. Those research tools
have not been migrated into Quetzal; see [the research guide](../../research.md).
The research helper's decompile action is read-only and performs no reanalysis.
The initial import completed successfully; the standalone Java query script
also compiled and decompiled the listed functions successfully.


## Quetzal integration

The TDU adapter now runs under `cmd/quetzal` alongside the existing 25 to Life
adapter, selected with `QUETZAL_GAMES`. Shared PRUDP, Quazal authentication and
account storage live in `internal/protocol/`; TDU-specific native master/proxy,
peer crypto and edNet remain in `internal/games/tdu/`. State, captures and
listeners are scoped to the enabled tenant. The stock directory handoff is now
proven through the master reply’s second IPv4 and `0x377b80`; see
[startup-handoff.md](startup-handoff.md). Native lifecycle/session discovery,
presence and Quazal-assisted introductions are implemented, along with
authenticated challenge/statistics storage. See [sessionmaster.md](sessionmaster.md),
[presence.md](presence.md), [peer-connectivity.md](peer-connectivity.md) and
[title-services.md](title-services.md). Stock startup, shared cruising and races
still require retail validation. See [running.md](running.md).

## Original standalone milestone (historical)

Before migration, the standalone `cmd/tdu-server` composed persistent accounts, Quazal UDP
transport/RPC services and native master/proxy transport. Its edNet services
provided a read-only directory and the three startup resources. See
[running.md](running.md) for current Quetzal commands and explicit implementation limits.

Additional US main-ELF anchors recovered during runtime work:

| VA | Established behavior |
| --- | --- |
| `0x1e0630`, `0x212d90` | Startup enables stream-3 packet encryption and installs the default RC4 context |
| `0x220790` | LZO1X decoder; all nine sanitized compression fixtures match direct retail routine execution |
| `0x21a800`, `0x21aa80` | RMC length-prefix framing outside the protocol/call/method fields |
| `0x1ede90`, `0x1ee010` | RV list stores special-protocol IDs; unlisted ordinary protocols use the regular secure endpoint |
| `0x12b170`, `0x12b520` | Reliable native action wrapper, matching transaction replies and ACK |
| `0x460b90`, `0x460960` | Five-slot directory request and ordered reply reader |
| `0x4095c0`, `0x409300` | Thirty-nine compiled numeric network defaults and NETCONFIG/value-attribute reader |
| `0x461720` | PRIZES/PRIZE reader; zero PRIZE entries perform no money/car grants |
| `0x392910`, `0x390910`, `0x38db80` | Skinny/GamePS2.ini loader, Online/ORB command registration and tail-jump setter callback |

Protocol, persistence and lifecycle tests pass, including complete synthetic
Quazal signup/login through secure registration/bootstrap over real UDP, and
native DH/activation/directory/resource downloads over real UDP. `go vet` and the
15 Python tooling tests pass. Short frame/LZO fuzz runs completed 93,366 and
177,709 inputs respectively without failures. The Linux executable was built
and smoke-tested with all four sockets on temporary loopback ports, a real master
request, generated resource initialization and clean SIGTERM exit. The Docker
image and Compose configuration also build/validate successfully.

These are synthetic-client/runtime checks, not a successful retail session.
The initial `0.0.0.0:9000` descriptor is populated by the online-entry routine
from the native master reply's **DirectoryIP**, independently of the optional
INI ORB override. Native-executed checks cover this handoff and the Open/Get
resource codecs, including the actual reply-consumer branches. The earlier
negative ORB/script scan is superseded by the `0x377b80` writer, not a current
blocker. These checks and synthetic real-UDP tests still do not establish a
successful retail online session. See [startup-handoff.md](startup-handoff.md)
and [native-services.md](native-services.md).
