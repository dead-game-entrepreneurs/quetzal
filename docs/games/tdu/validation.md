# TDU backend validation — 2026-11-17

Target: US PS2 SLUS-214.90. Runtime: Quetzal tenant `tdu` on branch
`codex/tdu-multitenant`. Private disc/Ghidra evidence remains in sibling
`tdu-server`; the server does not load it.

## Completed checks

- `scripts/go test ./...` and `scripts/go vet ./...` pass across both titles and
  shared transports. Coverage was also collected before the final URL-format
  correction; no retail client was substituted for these synthetic tests.
- `scripts/go build -trimpath -o bin/quetzal ./cmd/quetzal` passes.
- `docker build --network=none -t quetzal:tdu-gameplay .` passes.
- `docker compose --env-file .env.example config --quiet` and `git diff --check`
  pass. Local documentation links resolve.
- Isolated Docker smoke with `--network none`, fresh private state and both
  tenants passes: authentication SYN/checksum for each title, native master proxy
  and directory IPv4 fields, health tenant inventory, TDU connectivity diagnostics
  and existing 25 to Life patch/HTTP routing. The optional 238-entry activity
  catalog loads successfully. The test container was removed; existing Quetzal,
  Angelware, DNS and DNAS services were not restarted or reconfigured.

The private smoke report is `.cache/smoke/gameplay-result.json`. Build output,
private state, metadata and local reports are ignored by version control.

## Real UDP integration scope

`TestTwoDriversDiscoverJoinLookupReportAndDisconnect` creates distinct accounts,
completes shared Quazal login/secure proof, native DH/activation, directory/time
lookup, USERMAP updates, nearby/map discovery and a common FreeRide registry
session. It verifies peer lookup and native-socket NAT instructions, durable
statistics, challenge upload/download and cleanup after host departure.

`TestTwoDriversHostBrowseJoinAndLeaveDriveIn` replaces a cruise record with a
race record, checks idempotent creation and Drive-In density, applies restrictions
and filtered browsing, then exercises the actual browser's **prejoin** probe form:
`udp:/address=...;port=...;RVCID=...`. A fresh USERMAP claim plus a recently returned
browser row permits the introduction; merely knowing the CID does not. It also
checks CID-only secure lookup, generic join, deletion/revocation and return to
cruising. Account PID, registered CID and profile identity are distinct in these
tests. Generic join is a recovered SDK/server path; an active stock caller is not
yet established.

Tests do not exchange driving/physics packets or execute a peer race. Clients
retain simulation, race synchronization and local progression.

## Independent native fixtures

Selected real retail routines execute in bounded Unicorn harnesses:

- Initial master reply reader and stock startup continuation verify the directory
  IPv4 handoff; see [startup](startup-handoff.md).
- Actual SessionMaster serializers verify descriptors, asynchronous reply order,
  field widths and independent full-width profile identity; see
  [sessions](sessionmaster.md).
- USERMAP, nearby/map and activity-density request writers/consumers verify
  complete envelopes, map geometry, 210 + 238 catalog counts and the 449-cell
  activity representation; see [presence](presence.md).
- Native resource Open/Get senders and reply consumer verify string/buffer lengths,
  high-byte integers, error/retry paths and partial/final downloads; see
  [resource fixtures](../../../internal/games/tdu/ednet/testdata/README.md).
- Retail EventStats/GameStats traversal verifies full-width record fields; ordinary
  stream I/O is intercepted in this narrower harness. See
  [title fixtures](../../../internal/games/tdu/titledata/testdata/README.md).

Fixtures contain synthetic data and provenance, not game code or user captures.
The optional activity catalog contains only protocol identifiers/indices.

## Limits

No successful stock-client online entry, two-client cruising or race completion
has been observed. First retail acceptance should fresh-boot the documented disc,
retain the existing DNAS route and capture the first unsupported exchange; see
[running](running.md) and [milestones](roadmap.md).

Ranking, resource and activity-count policies are explicit replacement policies
where original server logic is unknown. Native shared-key/CID claims are not
cryptographic account proof. Persistent data uses independently verified Quazal
identity. Direct-peer traversal still needs real NAT testing; TDU relay and full
social/SDK coverage are not established. The pinned Alpine toolchain cannot run
Go's race detector without a CGO toolchain; normal concurrent reservation and
lifecycle regressions pass.

## Hosted-test preparation follow-up

The source review fixed loss of legacy challenge-text bytes across JSON storage.
Disk version 2 preserves name/description byte strings; valid v1 state migrates
only on a successful write. Upload/restart/list and failed-commit regressions pass.
Quazal RPC logs now include call/peer/endpoint and account/connection IDs. Native
service debug events include envelope/action/transaction/port/generation scalars
without payloads; separate actions rejected on one port remain distinguishable.

A cancellation-before-Serve timing issue found by the full suite is fixed in both
transports. The affected two-driver/startup integration checks passed ten repeated
runs, alongside the full regression suite, vet and build. The follow-up image is
`quetzal:tdu-beta`; its isolated smoke also enabled debug logging and captures,
verified private files for both tenants, checked title-data version 2 and retained
the legacy 25 to Life routes. Report: `.cache/smoke/beta-result.json`.

Read-only SSH inspection of RackNerd found a healthy existing 25 to Life build
with debug/capture already enabled. TDU ports and the proposed relay pool have no
listeners, but UFW still needs UDP 8122/8124 and 30400–30527 allowances. The old relay
rule already allows UDP 30200/30201. The actual public DNS resolver has the 25 to Life
mapping and lacks tdulive.quazal.net. No remote configuration was changed. See
[deployment](../../deployment.md#preparing-a-combined-hosted-test) and the private
`work/deployment/racknerd-readiness.json` audit before rollout.
