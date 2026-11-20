# Local protocol research

Research helpers are separate from the Go server. Use your own US PS2 disc of
25 to Life; no game assets or decompiler output are distributed here.

Requirements: Python 3, 7-Zip, `readelf`, and Ghidra with an Emotion Engine
extension providing `r5900:LE:32:default`. The research wrapper defaults to
Ghidra 12.1.4 under `~/.local/opt/`; set `GHIDRA_INSTALL_DIR` for another location.

```sh
tools/extract_25tolife.sh '/path/to/25 to Life (USA).iso'
tools/ghidra.sh import
tools/ghidra.sh decompile work/25tolife/connect.c refs:ttlliveps2.quazal.net
tools/ghidra.sh decompile work/25tolife/login.c refs:JobBackEndServicesLogin::
```

The project is `ghidra/quetzal.gpr`. Import once; `import -overwrite` deliberately
replaces the imported program and saved annotations. Generated assets, databases,
caches and exports remain in ignored `disc/`, `ghidra/` and `work/` paths.
Only the Java research scripts are source files.

The extraction helper also reads `MAIN.HOG` and extracts patch modules.
`tools/ghidra.sh import-patcher` prepares a local analysis copy of `PATCHLIB.REL`,
resolves imports to named synthetic stubs and adds `PATCHLIB.bin` to the project.
Use `decompile-patcher` to inspect it. This analyzes the stock update-check
module; it does not generate or install client patches.

`python3 tools/inspect_25tolife_updater.py /path/to/SLUS_210.16 /path/to/MAIN.HOG`
prints a read-only audit of the researched US build's native module-loader
calls, menu linkage, configuration commands and disc-module imports. The
[native-updater notes](games/25tolife/native-updater.md) explain those observations
and the limits of using this updater to replace the compiled MOTD menu code.

The exporter accepts addresses, `code:0xADDRESS`, `refs:TEXT`,
`addrrefs:0xADDRESS`, `callers:0xADDRESS`, and `name:SUBSTRING`. Output includes
the executable hash, language, references and direct calls. `code:` discovers
an unanalyzed function in the temporary read-only session. References and
instructions must be checked alongside approximate decompiler types.

The [build notes](games/25tolife/analysis.md) record executable provenance and
confirmed behavior. Add wire layouts only after checking request writers,
reply dispatchers and identity/authority rules. Use synthetic fixtures for
regressions, never real credentials, account records or full personal captures.

## TDU evidence workspace

TDU's evidence maps and exact US build hashes are in
[docs/games/tdu](games/tdu/analysis.md), starting with the
[online architecture map](games/tdu/online-map.md). Its runtime is now the
Quetzal `tdu` adapter. The historical `tools/research.py`, native-routine harness
and isolated Ghidra projects remain in the sibling `tdu-server` research workspace.
TDU notes mark commands requiring that workspace. Preserve its private evidence
and annotations there; no private binaries, package payloads or captures were
migrated into this repository. Quetzal never reads that workspace at runtime.

Further TDU research should resolve the initial nonzero directory IPv4 handoff
before claiming stock online entry, then validate discovery/session/peer admission
separately from cruising and racing. DNAS continues through the existing working
revival route and is outside this remaining TDU work.
