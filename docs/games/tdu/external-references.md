# External cross-checks

Research provenance: these findings were produced in the sibling `tdu-server`
workspace. Runtime package paths below refer to Quetzal; `tools/`, private
`work/` evidence and Ghidra projects refer to that original research workspace.
Keep those private files there; Quetzal has no runtime dependency on them.
See [running.md](running.md) for current Quetzal operation.

The PS2 disc, Ghidra analysis and native-routine tests provide wire-layout
and behavior evidence. PSHome-MultiServer was also explicitly consulted online
as an implementation reference. Credit to GitHubProUser67 and its contributors.
The earlier blanket statement that no implementation code was copied has been
withdrawn: the available provenance records establish consultation, not a
complete comparison of potentially adapted logic. See the repository-wide
[acknowledgments and licensing notes](../../../THIRD_PARTY.md).

[PSHome-MultiServer](https://github.com/GitHubProUser67/PSHome-MultiServer) exposes
separate Quazal and Eden/edNet components. The inspected snapshot is commit
`8778e985e4fbbec9ee8f29d007d256dd1df5814f`.

- Its [Lair account method](https://github.com/GitHubProUser67/PSHome-MultiServer/blob/8778e985e4fbbec9ee8f29d007d256dd1df5814f/Servers/QuazalServer/RDVServices/GameServices/v2Services/LairAccountManagementProtocolService.cs)
  returns a four-byte IPv4 value. This agrees with the PS2 method-104/1 decoder,
  while the PS2's later native call chain establishes the bootstrap role.
- Its [native master](https://github.com/GitHubProUser67/PSHome-MultiServer/blob/8778e985e4fbbec9ee8f29d007d256dd1df5814f/Servers/EdenServer/AMHLair/TDUMasterServer.cs)
  provides a small bootstrap exchange. This is a useful comparison target, not
  a substitute for the PS2 constructor and reply-handler evidence.
- Its [native proxy handler](https://github.com/GitHubProUser67/PSHome-MultiServer/blob/8778e985e4fbbec9ee8f29d007d256dd1df5814f/Servers/EdenServer/EdNet/Messages/Proxy/ToProxy.cs)
  contains a different version-gated security flow. The accepted version list
  observed in that source includes `MC1.45A`, `MC1.66A` and `[VED]TDU2`; the
  presence of generic TDU support does not establish PS2 compatibility.

The original tool records also identify the following requested/read reference
paths under the same pinned revision:

- `Servers/QuazalServer/AtariMelbourneHouse/AmhLairProxy.cs`
- `Servers/EdenServer/EdNet/ProxyServer.cs`
- `Servers/EdenServer/EdNet/ORBServer.cs`
- `Servers/EdenServer/static/Eden/StaticUserHostedFiles/1229391828/Core/netconfig.xml`
- `Servers/QuazalServer/RDVServices/DDL/Models/ChallengeStoreProtocol/ChallengeFileStats.cs`
- `Servers/QuazalServer/RDVServices/DDL/Models/EventStatsProtocol/EventStats.cs`
- `Servers/QuazalServer/RDVServices/DDL/Models/EventStatsProtocol/GameStats.cs`
- `Servers/QuazalServer/RDVServices/GameServices/v2Services/ChallengeStoreProtocol.cs`
- `Servers/QuazalServer/RDVServices/GameServices/v2Services/EventStatsProtocol.cs`

The records include attempted fetches as well as source inspection; listing a
path does not establish that its contents were successfully downloaded or used.
The source-tree index survives in ignored research `work/references/`; not all
previously described downloaded files were present during this review.

## Implementation areas and review boundaries

| Quetzal area | Recorded external comparison | Independent evidence / remaining limit |
| --- | --- | --- |
| `internal/games/tdu/game.go`, protocol 104/1 | Lair account/bootstrap method | Retail response-reader and socket-destination fixture establish the PS2 raw address byte order. Shared method semantics do not establish whether particular implementation logic was adapted. |
| `internal/games/tdu/proxycrypto/master.go` | Native master | PS2 reader requires a directory IPv4 in the second reply word; the pinned reference calls that field an encryption-key setting. |
| `internal/games/tdu/nativeproxy`, `proxycrypto`, `ednet` | Eden proxy/ORB message flow | The inspected reference proxy has a different version-gated flow. Native PS2 fixtures cover selected handshakes, envelopes and resources, not every source-provenance question. |
| `internal/games/tdu/titledata` | Challenge-store, EventStats and GameStats definitions | Public definitions were consulted for field/service names alongside recovered PS2 serializers. This use must be credited even without a confirmed verbatim copy. |

## Licensing

The pinned upstream [LICENSE](https://github.com/GitHubProUser67/PSHome-MultiServer/blob/8778e985e4fbbec9ee8f29d007d256dd1df5814f/LICENSE)
contains GNU GPL version 3. Attribution alone does not license copied or adapted
implementation. This reference inventory does not certify a clean-room
implementation or decide the licensing of all Quetzal code. Specific similarity
reports need a source-by-source comparison and appropriate notice/license
handling. `edNet` itself is also present in the original PS2 executable.
