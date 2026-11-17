# Revival milestones and validation

Runtime lives in Quetzal's TDU tenant. Private ISO, Ghidra projects, captures and
research scripts remain in the sibling `tdu-server` workspace. No game data is a
runtime dependency. See [running](running.md) for deployment and state paths.

## Implemented backend

- Shared Quazal authentication, signup, service tickets and secure registration,
  with isolated TDU accounts/keys and the title's wire settings.
- Native master assignment, signed DH, encrypted activation, replay handling and
  the stock directory-IPv4 handoff. Selected native readers execute successfully
  against sanitized fixtures; see [startup](startup-handoff.md).
- Action directory, generated network defaults, no-awards prize policy and a
  persistent peer-wrapper key. Only implemented actions are advertised.
- Bounded, transport-owned SessionMaster records, lookup/search, host updates,
  reservations, restrictions, cleanup and directory time discovery; see the
  [exact action inventory and policies](sessionmaster.md).
- Transient player positions, nearby-player discovery and map density, with
  recovered stock map dimensions and native-sender fixtures; see
  [presence](presence.md). Activity-density wire support does not prove the
  original server's activity aggregation policy.
- Authenticated Quazal-to-native peer lookup and direct NAT instructions gated by
  current session membership or recent browser discovery; see [peer connectivity](peer-connectivity.md).
- Private, atomic storage for reported statistics and challenge uploads, with
  bounded queries/downloads; see [title services](title-services.md). Original
  ranking formulas and opaque file metadata remain explicitly unproven.
- Multi-tenant listener/relay reservation checks, isolated state/captures and
  lifecycle cleanup while preserving 25 to Life's existing routing.

The combined synthetic-driver test exercises real UDP account login, native
startup, discovery, joining, peer lookup/NAT instructions, reporting, challenge
transfer and departure cleanup. Other tests independently check codecs, authority,
limits, persistence failures and malformed input. These establish backend
behavior, not stock-client playability.

## Next acceptance: fresh stock online entry

Keep the user's existing DNAS revival route. Fresh-boot US SLUS-214.90 rather than
resuming a cached savestate. Capture authentication on UDP30200, secure service on
30201, native master8124 and proxy8122. Check each startup callback through
resource loading and entry into the connected world. Address the first actual
unsupported exchange with a bounded codec and a sanitized regression fixture.

Success means one unmodified client completes its normal online-entry gates
without forced UI state. A ready log, health response or successful DH handshake
alone does not meet this milestone.

## Two-client cruising and racing

Use distinct accounts and clients. Verify mutual nearby discovery, common group
identity, direct peer login and bidirectional car updates while moving. Then
verify departure/rejoin, host loss, full groups and spatial group transitions.
Native claim ownership is scoped to a transport endpoint/generation; it is not a
cryptographic proof that a native connection-ID or presence claim owns an account. Persistent account
and title data require independently verified Quazal identity.

Test a Drive-In/custom race from browsing and joining through start, finish and
return to cruising. The backend registers sessions and persists client reports;
stock clients own driving, peer synchronization and race simulation. Do not add
server simulation merely to fill gaps in the recovered service protocol.

Success means two stock clients complete these flows, including cleanup and a
fresh rejoin. Listing an eight-slot room does not establish playable cruising or
racing. Record real-console and PCSX2 Sockets results separately.

## Remaining compatibility work

- Confirm all client-facing field meanings and original activity aggregation;
  prefer explicit replacement policies over fabricated historical semantics.
- Validate direct peer setup with different public addresses/NAT conditions.
  Shared 25 to Life relay code does not establish TDU relay compatibility.
- Implement additional social/title operations only from recovered or captured
  schemas; unsupported methods must not return invented successful results.
- Validate report/ranking behavior in the stock UI and bound any additional
  uploads. Current rankings are documented replacement policies, not recovered
  original scoring or authoritative result verification.
- Exercise restart/backup/recovery with real client state and maintain tenant
  isolation. Do not merge TDU identities, keys or files into 25 to Life state.

No successful retail online-entry, shared-cruising or racing milestone is claimed
yet. The next useful evidence is a fresh stock-client trace against this backend.
