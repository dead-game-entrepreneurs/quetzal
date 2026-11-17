# External cross-checks

Research provenance: these findings were produced in the sibling `tdu-server`
workspace. Runtime package paths below refer to Quetzal; `tools/`, private
`work/` evidence and Ghidra projects refer to that original research workspace.
Keep those private files there; Quetzal has no runtime dependency on them.
See [running.md](running.md) for current Quetzal operation.

The PS2 disc and Ghidra analysis remain the source for this project's wire
layouts. Public code was inspected as a reference only; no implementation code
was copied into the standalone server/tools.

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

Selected downloaded reference files and the source-tree index are retained in
ignored `work/references/` with the commit identity. Any future reuse of external
implementation code requires checking its license and attribution separately.
