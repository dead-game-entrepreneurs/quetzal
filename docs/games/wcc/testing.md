# World Championship Cards — retail test checklist

This checklist covers PCSX2 sessions with quetzal running inside WSL on one of
the PCs. Results so far are in the [WCC notes](analysis.md).

## 1. Start the server (WSL)

quetzal is Linux-only, and it refuses key and account files whose permissions
are not private. Keep its state on the WSL filesystem, not under `/mnt/c`.

Build `bin/quetzal` (ignored by Git) with
`GOOS=linux CGO_ENABLED=0 go build -o bin/quetzal ./cmd/quetzal`, then start it
from a WSL shell with the WCC tenant only, for example:

```sh
QUETZAL_GAMES=wcc QUETZAL_BIND=0.0.0.0 QUETZAL_PUBLIC_IP=<address PCSX2 reaches> \
QUETZAL_STATE_DIR=~/quetzal-state QUETZAL_CAPTURE_DIR=~/quetzal-state/captures \
QUETZAL_LOG_LEVEL=debug ./bin/quetzal
```

In WSL's default NAT mode the reachable address is the WSL VM address
(`hostname -I`), which can change after a reboot. In mirrored mode it is the
PC's LAN address. Check `http://<address>:8082/healthz` lists UDP 30280/30281.

## 2. Point the game at it

- Add `wcclive.quazal.net` → that address to the DNS route you already use for
  DNAS (resolver or PCSX2's internal DNS host list). Keep the DNAS entries.
- PCSX2: Sockets networking, as in the 25 to Life tests. No patches or cheats.
- Every PC that may host must accept inbound UDP for PCSX2 (port 9103 in the
  tests). Joiners connect straight to the host's PCSX2 after the server's 11/3
  answer; a blocked host shows as 11/3 followed by a ~30 second wait and no 21/11.
  Since the server now sends the host a 3/2 probe on each lookup, look for
  `wcc probe queued` and `quazal probe initiation acknowledged`. If the host
  acknowledges and the join still times out, allow PCSX2 on Private networks
  in Windows Firewall on the host PC.

## 3. Test in this order

| Step | Expect in the server log |
| --- | --- |
| Open the online menu, create an account | 10/1 and 10/3 as `guest`, secure 11/1, then 25/1 completed |
| Sign in with it | 10/1 with the new name, 11/1, 25/17, 18/1 completed |
| Restart the game and sign in again | Same PID as before (watch for login failures: the password is derived on the console) |
| Host a table | `wcc gathering registered`, then 21/11, 21/26 |
| Second PCSX2: browse and join | 21/17 lists the table, 11/3 with `wcc probe queued`, then 21/11 `wcc participant joined` |
| Third PCSX2 joins | 3/1 `quazal probe initiation queued` with `targets: 1`, then 21/11 with 3 participants |
| Host leaves mid-game | 21/36 `wcc gathering migrated`, `wcc ownership notification queued`, then 21/12; the new owner sends 21/27 or 21/4 |
| Host's connection drops instead | `wcc gathering awaiting migration`; a remaining player claims it with 21/36 within 30 seconds |

`/games/wcc/connectivity` shows live players and tables by type.

## 4. What to send back

- Server log lines from the session, especially any `wcc unsupported rpc`,
  `wcc gathering rejected` or `quazal RPC rejected`.
- The game's on-screen error text and the step where it appeared.
- If nothing reaches the server at all, say so: that points to DNS/DNAS or the
  transport (checksum seed 0x2b, PRUDP header), not matchmaking.

Packet captures under `captures/` contain account secrets; share excerpts only.
