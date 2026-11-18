# Host migration and stale sessions

The stock US PS2 client elects hosts through its native session layer. Its
`HostMigrationExtension` then calls matchmaking `21/27 UpdateSessionURL` with
the existing gathering ID and an **empty string**. This is an intentional retail
request, emitted at `0x002fe8c8` through `0x005378b8` and `0x0053a1c0` in
`SLUS_210.16`; it is not a malformed URL. The native callback is `0x002fe628`.

## Gathering lifecycle

`50/1 EndParticipation`, owner `21/12` cancellation and a transport disconnect
remove the departing connection's participation. A TTLSession with registered
participants remaining enters a 30-second handoff window:

- Keep its gathering ID, record type, settings and private-room policy. Exact
  remaining participant connections can still retrieve it through `21/21`,
  avoiding an empty record while the native migration callback runs.
- Withdraw the departed host's endpoint and authority immediately. Hide the
  gathering from browse/list results and reject new participants during handoff.
- Accept `21/27` from a registered connection that already participates in that
  gathering. Resolve the empty URL to its authenticated observed StationURL,
  transfer authoritative owner/host identities and resume discovery. An active
  owner's room cannot be claimed by an ordinary participant.
- Authorize subsequent updates by the exact new owner connection. Rewrite
  supplied owner/host fields to that identity, because the stock successor can
  still have the original owner's PID in its cached record. Invalid record
  types and zero/unknown gathering IDs remain invalid.
- Remove the gathering if all remaining participants leave or the handoff
  expires. An idle transport tick performs expiry even without client requests;
  repeated old-host cleanup cannot extend the deadline.

The server validates membership and the vacant host role. It does not implement
or verify the native election itself. This path handles the observed ordering
where backend host departure precedes the successor's callback; a claim against
a still-owned gathering is rejected. Abrupt network loss still depends on
transport disconnect/timeout detection. Generic SDK `21/36` ownership migration
remains unsupported and is separate from this observed stock extension path.

`21/2 UnregisterGathering` remains explicit deletion. TTLRounds retain their
existing round-end reporting window rather than becoming discoverable migration
vacancies. Cached reported player counts are capped by the current backend
participants plus the host; a lower native count remains authoritative for peers
that requested participation but did not complete a native join.

## Connections and relay cleanup

25 to Life enables one registered secure connection per named account. A new
ticket-proven connection must complete valid registration before the older
registration is retired. Its rooms/membership are cleaned up, queued traversal
instructions are removed and cached RPCs from that connection cannot restore its
authority. A late disconnect for the old connection cannot affect the new one.
Other accounts and concurrent guest registrations are unaffected. This is a
title policy; other adapters keep the shared protocol's default behavior.

Host departure retires only that player's relay routes, preserving proved
survivor-to-survivor paths. Complete gathering removal retires all routes scoped
to it, including guest-to-guest candidates. Proved routes keep the existing
five-second drain for native leave packets, then release their resources.
Expired/deleted gatherings cannot grant new introductions or resurrect routes.

Logs include `matchmaking host migration pending`, `matchmaking host migrated`,
`matchmaking host update rejected`, and removal reasons. Room snapshots include
`migration_pending`; connection replacement logs the reason
`replaced by fresh account registration`. No raw URLs, room names or account
fields are added to these matchmaking diagnostics.

## Validation

Regression tests cover the captured leave → lookup → empty-URL update sequence,
public/private handoff, exact connection authorization, stale cached identities
and player counts, automatic expiry, late cleanup, fresh registration and RPC
replays. A UDP test forwards native payloads over an existing survivor relay
after host departure and verifies final route retirement. Existing round stats,
social, traversal and other-adapter tests remain required.

After deployment, fully reconnect stock clients and create a fresh room. With
two or more players joined, leave online mode on the host and verify that the
surviving client retains the gathering and can continue hosting. With three
players, also verify the two survivors still exchange movement/traffic over a
relay path and another client can find/join the new host. Repeat with a private
room and with a fresh login replacing an old connection. These backend tests do
not substitute for stock-game confirmation of native election and continued
gameplay. No client patches are used.
