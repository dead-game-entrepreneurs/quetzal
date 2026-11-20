# 25 to Life native PS2 updater

The retail game contains a real-console update path. The original
[PS2 manual, printed page 10](https://support.na.square-enix.com/document/manual/982/25_to_Life_PS2.pdf)
describes downloading an update during login and storing it on the memory card.
This is separate from PCSX2 cheats or emulator patch files.

The US executable and modules were traced in Ghidra. An offline harness also
executed selected retail archive-reader routines against synthetic text data.
**A custom update has not been downloaded, installed, or verified on a PS2.**
The production endpoint continues to return the existing no-update HTTP 404.

## Download, storage and activation

Main-executable addresses refer to the SLUS-210.16 build identified in
[analysis.md](analysis.md). Module addresses below are relative to the module's
analysis base of zero, not fixed console RAM addresses.

| Stage | Verified behavior and location |
| --- | --- |
| Start | `0x0018b1b0` loads `PatchLib.rel`, or the separate PAL module. `PatchExt.rel` was loaded during game initialization at `0x00232bc0`. |
| Check the card | `0x0018b368` searches for `*.pat`; `0x0018b440` and `0x0018b570` query and read the saved update through the game's card manager. |
| Read a saved update | `0x0018b5d0` calls `SetPatch` and `PreparePatch`; `0x0018b698` verifies the extracted archive and obtains its version. |
| Check the server | `0x0018b810` requests `/TTL/final-version.txt`. The parser is `Patcher::_ThreadParseVersion`, `PATCHLIB+0x1838`. |
| Download | `DownloadPatch`, `PATCHLIB+0x10f8`, schedules HTTP fetching. Worker command 2 fetches the file and prepares its local stored representation. |
| Save | `0x0018bc28` obtains the stored size; `0x0018bd10` writes the buffer as `patch` with the `.pat` extension through the normal memory-card manager. |
| Extract and activate | `0x0018bd80` calls `PreparePatch`, then `0x00449ec8` creates the active archive extractor. The existing-update path at `0x0018b880` also activates a previously read archive. |
| Apply configuration | Both activation paths call `0x0037c7c0`, which reloads `NetworkConfig` using the resource/configuration readers. |
| Finish | Completion state `0x0018bee8` pops the updater handler. Its exit callback `0x0018b030` releases the active archive through `0x00449f88` and unloads the patcher if still loaded. |

The manifest recognizes `Host`, `Patch`, `Version`, `RawSize`, `StoreSize`,
`ClearSize`, `Description` and `Mandatory`. A version **different from** the
saved version can select an update; the comparison is not strictly greater-than.
The HTTP 404 path is explicitly converted into successful completion without a
new update. Download bytes, saved bytes and extracted archive bytes are distinct
representations; serving a plain archive with an arbitrary manifest does not
verify the whole installation path.

## DNAS login and update-file preparation are separate

The deployed DNS forwarding already supplies the revival DNAS login service.
Nothing in this investigation requires changing that forwarding.

The updater additionally calls local SDK functions while preparing a file:

- `Patcher::_ThreadEncryptLocal` (`PATCHLIB+0x1bd8`) calls
  `sceDNAS2InstPersonalizeDataLength_nohdd` and
  `sceDNAS2InstPersonalizeData_nohdd` before saving.
- `Patcher::_ThreadDecryptLocal` (`PATCHLIB+0x1cd0`) calls
  `sceDNAS2InstExtractDataLength_nohdd` and
  `sceDNAS2InstExtractData_nohdd` before the archive is used.

Read-only tracing of the runtime-decoded validation code identifies RSA public-key
processing at `PATCHLIB+0x1e540`, called through `+0xd3f0` during the header
processing at `+0x1a130`. It uses the module's `AI_PKCS_RSAPublic` and
`KI_RSAPublicBER` routines. This is a local file-format/validation step, not
another failed DNAS login or a missing DNS record. Constructing a new package
accepted by this complete preparation path remains unverified; an original
downloaded update would help validate its outer representation.

## Extracted archive format

`PATCHEXT` verifies a little-endian format marker `0x103`, then an MD5 digest of
all bytes from offset `0x14` onward. Those bytes start with the update version
and entry count. Each entry contains five 32-bit words, original and replacement
MD5 digests, a one-byte filename length and the filename.

`ContentTable::FindEntry` (`PATCHEXT+0x660`) normalizes ASCII case and slash
direction while finding a resource. `TOCEntry::ApplyPatch` (`+0x128`) inflates a
command stream that can copy from the original resource, copy from already
reconstructed output, or emit literal bytes. Its replacement digest check is
separate from the archive digest.

The offline native-routine checks passed:

- Accept a synthetic archive and locate its resource with the expected size.
- Reconstruct the compressed text through `PatchExtractor::PatchItem`.
- Reject a corrupt archive digest.
- Reject an incorrect replacement digest even when the outer archive digest is
  valid.

These checks establish the inner archive format. They do **not** establish HTTP
download, SDK personalization, memory-card installation or hardware execution.
The harness supplies platform services and zlib; it does not ship a change to
PCSX2 or to the game.

## Applicability to the disappearing MOTD

The [MOTD lifecycle trace](rankings.md#message-disappears-after-leaving-a-room)
places the bug in compiled multiplayer-menu entry code at `0x0018f9a8`. It clears
the widgets; the normal completion callback at `0x0018fcd8` displays the cached
message. A fix would need to preserve or repopulate that display when entering
the menu again.

The updater's verified application path reloads configuration through a temporary
resource overlay. General file reads at `0x004495e8` and bundled resource reads
at `0x00396608` consult that overlay while it is active. The generic SNR2 loader
at `0x00430fd8` can read a replaced module and invoke its normal entry point via
`0x002738e0`; that alone does not cause the updater to replace an already loaded
main-executable function. `Patcher::_ApplyUpdateWork` (`PATCHLIB+0x1590`) is empty
in this build.

No intended menu-code replacement or MOTD refresh hook has been established in
this update flow. Consequently, native updates are supported by the original
PS2 game, but **using one for this MOTD fix remains unproven**. A complete result
needs an accepted outer package, a verified application path to the menu change,
and a real-console install/reboot/room-exit test. No emulator-specific patch or
custom client update has been installed as part of this research.
