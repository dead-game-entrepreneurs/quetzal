# Hosted connection and matchmaking diagnostics

`QUETZAL_LOG_LEVEL=debug` enables packet metadata and per-room search details.
Connection lifecycle, room lifecycle and search summaries are emitted at info
level. Configure these in the hosted `.env` and recreate the service to apply.

```sh
docker compose logs -f --no-log-prefix --tail=100 quetzal
docker compose logs --since=30m --no-log-prefix quetzal |
  jq 'select(.game == "25tolife" and (.msg | startswith("matchmaking")))'
```

Correlate `game`, `local_port`, `peer`, `pid` and `call_id`. Peer IDs are scoped
to a transport listener and reset on process restart. The `ready` event marks
the start of a runtime. Account names, passwords, profiles, room names and raw
StationURLs are not added to these diagnostic events.

| Event | Useful fields |
| --- | --- |
| `PRUDP connected` | Remote address, local port, peer and stream IDs |
| `PRUDP CONNECT rejected` | Endpoint and handshake rejection reason |
| `PRUDP disconnected` | Exact reason: peer exit, idle timeout, fragment timeout, exhausted retries, replacement or shutdown; pending/buffered counts |
| `quazal connection established` / `closed` | Auth/secure role, proof state, PID, CID and registered flag as available |
| `quazal RPC completed` / `rejected` | Game, endpoint, PID, peer, protocol/method, call ID and rejection reason |
| `matchmaking room registered` / `removed` | Room ID/type, owner PID/peer, settings, published endpoint flag and removal reason |
| `matchmaking participant joined` / `left` | Caller, room and updated backend member count |
| `matchmaking participation rejected` | Room missing, full, private without invitation or duplicate account connection |
| `matchmaking room updated` | Host-reported state and player/settings snapshot; debug level |
| `matchmaking search completed` | Normalized filters, total/matched rooms, returned room IDs and per-filter exclusion counts |
| `matchmaking browse candidate` | Room metadata and mismatched filter names; debug, capped at 32 candidates per search |
| `matchmaking search rejected` | Request/call identity, bounded size, query fingerprint and parsing reason |

Search fingerprints and level-name hashes correlate repeated requests without
logging arbitrary client text. Numeric game options and private/ranked flags
remain readable. Exclusion counts can overlap when one room fails several
filters. Search summaries describe backend replies; native peer connection and
client UI acceptance must be checked separately. Existing `native relay`
events record candidate allocation, activation, mapping proofs, traffic and
cleanup; an offered candidate alone does not establish that traffic was relayed.

The 25 to Life map-filtered browse builder can place `TTLSessions.levelName`
before `Gatherings.type`. These equality clauses are accepted in either order,
while retaining the required exact TTLSession type, field allowlist, duplicate
checks and typed values. This is a bounded parser, not execution of client SQL.

Raw private recordings are controlled separately by `QUETZAL_CAPTURE_DIR`.
They retain full backend payloads and rotate at 16 MiB with three backups per
tenant. They may contain credentials/profile data and must stay outside Git.
Use [deployment notes](deployment.md) for locations and retention details.
