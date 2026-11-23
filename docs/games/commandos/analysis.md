# Commandos Strike Force: US PS2 initial investigation

Static disc inspection and initial Ghidra analysis: 2026-11-18.

The continued sweep is documented in the [online service map](online-map.md),
[authentication reference](authentication.md), [matchmaking wire map](matchmaking.md)
and [reuse plan](reuse-plan.md). Those notes supersede the initial open questions
below while retaining this file's build provenance.

**Confirmed Quazal title:** this build contains both Net-Z gameplay middleware
and Rendez-Vous backend clients. This establishes a research target for Quetzal;
it does not establish compatibility with either existing game adapter. A separate
experimental adapter is now available; see [running.md](running.md). No retail
online test has been performed for Commandos.

## Build and endpoint

Source: locally supplied `Commandos - Strike Force (USA) (En,Fr,Es).iso`.
`SYSTEM.CNF` boots `SLUS_211.03`, reports version `1.02`, and selects NTSC.

The loose disc file `CONFIG/JUEGOVER.CFG`, lines 15–18, configures:

| Setting | Value |
| --- | --- |
| Rendez-Vous hostname | `cxliveSCEA.quazal.net` |
| Rendez-Vous port | `30060` |
| Rendez-Vous title password | `7fas5` |
| Maximum multiplayer players | `8` |

The password above is a shipped title setting, not a player credential. These
are static configuration values; tracing their consumers and the secure-service
handoff remains necessary before assigning listener ports or DNS rules.

| File | Bytes | SHA-256 |
| --- | ---: | --- |
| `SLUS_211.03` | 272856 | `a89f0d210d63476da9aa616f423258ef9c7bf39c9e0481d740c0bea4ab7a6801` |
| `COMMX_FR.ELF` | 299816 | `5a9f7005ccc5d3690524fca58b012aad13d1d06b064c3e268465b729e33f0231` |
| `FIN_RED.BIN` | 6996992 | `25f2d3ffb7f9ceb54635f7ac69bb8afb510a37e2c2cf6cad2e5243e5ef484e60` |
| `RVOUS_VF.BIN` | 152704 | `6d4b041b4ad896eebaa6ba8ff5eeb001cb852feadde64cd62baa116286648205` |
| `CONFIG/JUEGOVER.CFG` | 679 | `9e50f49e7129b11494234cf770604a7b44b92a36d06d1f6566bf9aacb26bfa40` |

## Middleware evidence

Offsets below are **file offsets**, independent of overlay address assumptions.

| File | Offset | Literal evidence |
| --- | --- | --- |
| `FIN_RED.BIN` | `0x6725d0` | `NetZ.cpp` |
| `FIN_RED.BIN` | `0x678e70` | `Quazal Net-Z` |
| `FIN_RED.BIN` | `0x673fd0` | `PRUDPTransport.cpp` |
| `RVOUS_VF.BIN` | `0x235c0` | `Quazal Rendez-Vous` |
| `RVOUS_VF.BIN` | `0x22fe0` | `Quazal::TicketGrantingProtocolClient` |
| `RVOUS_VF.BIN` | `0x23420` | `Quazal::SecureConnectionProtocolClient` |
| `RVOUS_VF.BIN` | `0x23880` | `Quazal::MatchMakingProtocolClient` |
| `RVOUS_VF.BIN` | `0x23960` | `Quazal::MatchMakingProtocolExtClient` |
| `RVOUS_VF.BIN` | `0x23b50` | `Quazal::NATTraversalProtocolClient` |
| `RVOUS_VF.BIN` | `0x23fb0` | `Quazal::AccountManagementProtocolClient` |

`FIN_RED.BIN` also contains RC4, keyed checksum, LZO compression, voice channel
and host migration classes. These identify linked capabilities, not verified
wire formats or retail feature usage. `IRX/QNET.IRX` is present (13988 bytes).

## Ghidra starting point

The small boot ELF alone omits the online implementation. The disc contains
`MWo3` overlays; do not treat their headers as instructions or strip the header
without adjusting addresses.

| Overlay | Header base at file `+0x08` | Initial observation |
| --- | --- | --- |
| `FINAL.BIN` | `0x150b00` | Named by the retail boot ELF |
| `FIN_RED.BIN` | `0x151e80` | Named by `COMMX_FR.ELF`; contains Net-Z |
| `RVOUS_VF.BIN` | `0x8c0480` | Contains Rendez-Vous |
| `CYBOR_VF.BIN` | `0x8c0480` | Shares the Rendez-Vous base; analyze separately |

In `COMMX_FR.ELF`, `0x11b160` passes `FIN_RED.BIN` to loader wrapper
`0x100230`. It reads the file through `0x100af0` into the pointer stored at
`0x146a34`, whose value is `0x151e80`. The read starts at file offset zero.
Thus FIN_RED's mapping includes its header: `VA = 0x151e80 + file offset`.
`0x11b0e0` clears trailing memory using header `+0x14` and passes the values
at header `+0x18` and `+0x1c` to another routine.

The initial Rendez-Vous import maps the complete file at header base
`0x8c0480`. Its internal pointers and string references agree with this map.
Cross-overlay calls remain unresolved in this isolated import; a combined
bootstrap/FIN_RED/Rendez-Vous memory map is needed for full call analysis.

Initial anchors under that Rendez-Vous mapping:

- `0x8e3a40`: product string `Quazal Rendez-Vous`, referenced by `0x8d0a00`.
- `0x8c2810`: validates login state and schedules the authentication-connection
  stage. Its callback pointer at `0x8e28c8` names `0x8c28e0`; distinguish the
  scheduling routine from the actual connection stage.
- `0x8e2920`: authentication URL format
  `prudp:/address=%s;port=%d;stream=%d;sid=%d;type=%d`.
- `0x8c28e0`: formats that authentication URL with the job's hostname/port
  and stream `3`, SID `1`, type `2`, then calls endpoint connection routine
  `0x66fea0` (outside this isolated overlay).
- `0x8e2ad0`: secure-service URL format, including PID and CID fields.

## Local artifacts and next pass

Private extraction is under `disc/commandos/`; Ghidra project
`ghidra/commandos.gpr` retains the initial isolated imports. The continued
investigation uses `ghidra/commandos-online.gpr`, combining the bootstrap,
FIN_RED and RVOUS images with `r5900:LE:32:default`. Logs, hashes, strings and selected decompilations
are under `work/commandos/`. All these locations are ignored by Git.
No game assets or decompiler exports belong in contributions.

To reproduce the selected-file extraction:

```sh
mkdir -p disc/commandos work/commandos
7z x -y -odisc/commandos '/path/to/Commandos - Strike Force (USA) (En,Fr,Es).iso' \
  SLUS_211.03 SYSTEM.CNF COMMX_FR.ELF FINAL.BIN FIN_RED.BIN \
  RVOUS_VF.BIN CYBOR_VF.BIN 'CONFIG/*' IRX/QNET.IRX
```

For the combined image, use `tools/extract_commandos.sh`, then
`tools/ghidra.sh import-commandos` and `decompile-commandos`. The latter always
queries the combined image read-only. See [reproduction commands](reuse-plan.md#reproducing-the-research)
and the current transport/retail-validation gaps in the linked service maps.
