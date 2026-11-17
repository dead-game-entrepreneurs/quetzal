# Testing Test Drive Unlimited PS2

This backend targets the US disc **SLUS-214.90**. It is an experimental community
test: account, discovery and session services are implemented, but no stock
client has yet completed online entry, shared cruising or a race against it.
Other regional builds have not been verified. Follow the test announcement's
server/DNS details; the server operator must first enable the `tdu` tenant and
its network routes.

## Connect

1. Use the US retail game on a PS2 or PCSX2 with Sockets networking. Use the DNS
   resolver provided by the host in the game's network configuration.
2. Fresh-boot the game. Avoid an emulator save state containing an old connection.
3. Open the normal online mode and create or sign into a game account. Use a
   password unique to this revival service. DNAS uses the host's existing revival
   route; no new certificate installation or disc patch is part of this setup.
4. Record the exact menu reached and the first error, hang or disconnection. A
   successful login alone does not confirm that the shared world is connected.

## Useful testing sequence

Start with one fresh client reaching the connected world. With two distinct
accounts, check whether each player appears nearby, can join the same cruising
group, sees the other car move and remains connected while driving. Then test a
Drive-In/custom race from browsing and joining through start, finish and return
to cruising. Finally, check host departure, reconnection and fresh room creation.

The game runs driving, peer synchronization and local progression. The backend
registers sessions, introduces peers and stores reports. Direct peer connectivity
is implemented; TDU relay support and strict-NAT compatibility are not verified.
Two clients on one LAN and clients on different networks are separate test cases.

## Report an issue

Provide:

- Time and timezone, plus the tested server/build announcement.
- Disc ID/region and PS2 or PCSX2 version/network mode.
- Menu/action, exact error text and what happened immediately before it.
- For multiplayer, whether both players were on one LAN or different networks,
  and whether both saw the same behavior.

A screenshot of the game's error is useful. Do not post passwords, account files,
raw captures or private logs publicly. The operator already has structured debug
logs and, when enabled, private rotating packet captures. The timestamp and
steps let them locate the corresponding exchange. Share any additional private
material only through the host's chosen private reporting channel.

Operators: see [running](running.md), [deployment](../../deployment.md) and the
[validation report](validation.md) for ports, diagnostic routes and test limits.
