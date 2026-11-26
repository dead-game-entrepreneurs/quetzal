# Rainbow Six Vegas (PSP) — test guide

This covers the first in-game test: creating an account and logging in with
PPSSPP. Only the US disc (`ULUS-10206`) is supported; the EU build uses a
different access key and port.

## Server

Run quetzal with only this tenant from WSL:

```sh
work/rsvpsp/run-wsl.sh
```

It prints the address to use for `rsvegaspsp.quazal.net`, writes logs to
`work/rsvpsp/session/` and keeps state in `~/quetzal-state` on the Linux
filesystem (the tenant's own files are under `tenants/rsvpsp/`). Rebuild
`bin/quetzal` after code changes. Allow UDP 30380 and 30381 through the
firewall, as for WCC's ports.

## DNS

Add `rsvegaspsp.quazal.net` to your local resolver with the printed address
(see `configs/rsvpsp.dns.yaml` for a revive-dns rule). The game resolves the
name itself through PPSSPP, so the Windows DNS setting does not matter.

## PPSSPP

Every PC needs a DNS server it can reach that answers `rsvegaspsp.quazal.net`
with the server's address (a static entry on the router works). "State:
Unknown" for this game in the PPSSPP log is expected, even when it connects.
In testing, a second PC could not resolve the name until a PPSSPP bug that
added 12 zero bytes to its DNS requests was fixed; if one PC never reaches the
server while another does, check its DNS requests first.

Two PPSSPP instances on one PC cannot both go online: the game always uses
UDP 9103 (server connection) and 1000 (match socket). Use one instance per PC.

Tested target: PPSSPP 1.19 or later. Setting names can differ between versions.

1. Settings → Networking: turn on **Enable networking/WLAN**.
2. Under **Infrastructure**, leave DNS **Autoconfigure** on, then turn it off
   and enter your resolver's address as the **DNS server**. In testing,
   typing the DNS server without first having Autoconfigure on made PPSSPP
   crash when going online; doing it in this order worked.
3. Start the game, go online and pick the emulated network connection when
   asked.

## What to try

1. Create an account: fill in the whole form (username, password, email,
   secret question and answer, real name, gender, birthdate, country).
2. Log in with the new account.
3. With a second PC, host a match on one and join it from the other. Play
   through every round: the joiner should follow the host into each next
   round, and both should return to the lobby after the last one.
4. Friends: add the other player by name from the friends page. They should
   get a friend-request box; accepting puts each in the other's list. Try
   deleting, and joining a friend's game from the list.
5. Statistics: after a match, open STATISTICS and press Next through own,
   friends, participants (only in a game) and leaderboard. Rating starts at
   1000 and moves with rounds won; accuracy is hits over shots.
6. Note any error message and where it appears.

## What to look for in the log

- Account creation: guest login (`pid 100`), registration, then
  `quazal RPC completed` for `protocol 25`, `method 21`.
- If creation fails, the `quazal RPC rejected` line gives the reason. A
  profile-record rejection includes the record type and size, without
  logging personal fields or secret answers.
- Login: a 10/1 with the new name, a secure connection with its PID and
  registration.
- After login the menus call Spark 60/7 (statistics) and 60/10 (friends);
  these and the other Spark list queries (6, 8, 9, 17, 20, 21, 22) get empty
  lists for now.
- Creating a match logs `rsvpsp game created` with its ID and the host's
  addresses; joining logs `rsvpsp game joined`. Joining needs a second
  PPSSPP instance or PSP with its own account. Both must be able to reach the
  host's game port (1000 on the emulated PSP) directly.
- Between rounds: the host's 60/18 and a `rsvpsp game created` with
  `udp:/fullhouse` and the same game ID, then the joiner's 60/18 and
  `rsvpsp game search by description` with `found 1` (e.g. `(1)TRIS`).
- Friends: `rsvpsp friends` lines with the method (2 add, 3 accept, 4
  decline, 7 delete), `ok` and the event sent to the other player (2
  request, 1 accepted, 0 declined or deleted). Friendships are kept in
  `tenants/rsvpsp/friends.json`.
- Statistics: one `rsvpsp stats counted` line per player per round set,
  with the reported values and play time. Careers are kept in
  `tenants/rsvpsp/stats.json`.
- Creating a match starts with a public-URL probe from the game's own socket
  (`PRUDP USER request completed` at debug level, from port 1000).
- `rsvpsp unsupported rpc` lines list the protocol, method and argument size
  for calls the adapter does not answer yet, without logging payloads. A full
  match and its rounds produce none.

Captures under `work/rsvpsp/session/captures/` contain account secrets; do not
share them whole.
