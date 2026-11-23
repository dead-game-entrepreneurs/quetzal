# Commandos authentication and account management

All addresses use the combined US PS2 memory map in [analysis.md](analysis.md).
Findings are static unless explicitly identified as native checks.

## Title configuration and bootstrap principal

`0x50d640` registers configuration keys for hostname, access password and port.
The normal game login facade `0x461c80` consumes these settings, supplies the
access password through `0x8c1350`, and passes the user's name/password and
configured endpoint into `0x8c17f0` -> `0x8c1820`.

The configured title access password is `7fas5`. The account-creation facade
`0x465840` follows the same setup but passes `**CuentaCrearCSFv1**` as both
login username and login password. This is a shipped service credential used
to reach account creation. It is not a player's password or the PRUDP access key.

The shared Quazal service defaults to signup identity `guest` and password
`h7fyctiuucf`. Player names retain their 15-byte limit. Its configurable signup
policy now permits the separate 20-byte Commandos bootstrap name, password key
and creation parameter. This is not stored as an ordinary player account.

Keep bootstrap privileges restricted to account setup, and preserve separate
tenant state. A configurable title credential must not grant arbitrary game
RPCs or weaken named-player authentication.

## Login sequence and anchors

| Address | Role |
| --- | --- |
| `0x8c1820` | Constructs/schedules backend login job |
| `0x8c2810` | Validates state and schedules authentication connection |
| `0x8c28e0` | Builds auth PRUDP URL; endpoint connect through `0x66fea0` |
| `0x8cba30` | Initiates ticket-manager login; normal branch uses method 1 |
| `0x8c8f00` | Protocol 10/1 request writer |
| `0x8c9560` | Ticket-granting reply decoder |
| `0x8ca930` | Derives password key using `65000 + (PID & 1023)` MD5 iterations |
| `0x8caa30` | Signup/password-change initial hash: 65000 iterations |
| `0x8cbd40`, `0x8cbe80` | Key derivation and validation stages |
| `0x8cc2b0`, `0x8c8d70` | Acquire service ticket; protocol 10/3 writer |
| `0x8cc4b0` | Decrypts ticket envelope; reads 16-byte session key, destination PID, ticket Buffer |
| `0x8c2ff0` | Selects returned secure URL or constructs fallback |
| `0x8cc900`, `0x8ccc40` | Secure CONNECT ticket/challenge processing and nonce+1 verification |
| `0x8cd140` | Protocol 11/1 registration writer |

Authentication URL: `prudp:/address=%s;port=%d;stream=3;sid=1;type=2`.
The initial port is 30060. Secure fallback uses the original port plus 0, 1 or
2 depending on the SDK service PID, or takes the server-supplied URL from
connection data. Return the intended secure endpoint explicitly.

The ticket crypto path creates RC4 and keyed checksum objects using the same
key (`0x8ca640`), appends/authenticates the encrypted container, and decodes the
service ticket and challenge structures used by the existing shared crypto
package. `0x8cc900` reads three challenge u32s and replies with the incremented
nonce; `0x8ccc40` checks that reply. Keep ticket encryption separate from the
title-specific Net-Z gameplay cipher.

## Backend request/reply shapes

`S` = SDK String (u16 byte count, NUL included when nonempty), `U` = u32,
`B` = one-byte bool, `Buf` = u32-length-prefixed bytes, `L<T>` = u32 count plus
elements. The normal RMC envelope is outside these argument lists.

| Protocol/method | Request | Reply decoded by this build | Reuse |
| --- | --- | --- | --- |
| 10/1 login | S name | result U, PID U, validation Buf, RVConnectionData, S message | Shared login layout matches |
| 10/2 alternate login | S name | Same decoded layout as 10/1 | Linked alternate SDK path; no identified ordinary game requirement |
| 10/3 ticket | U source PID, U destination PID | result U, sealed-ticket Buf | Shared service-ticket layout |
| 11/1 register | L<StationURL> | result U, connection ID U, StationURL | Shared registration layout |
| 11/2 connection data | U connection selector, U PID selector | result U, ConnectionData | Linked SDK writer; absent from shared server |
| 25/1 create account | S name, S initial-hash hex, U parameter (0), S contact | result U | Shared field shape; bootstrap identity and parameter policy differ |
| 25/4 change password | S initial-hash hex | B success | Shared opt-in operation, not a qResult reply |
| 25/17 status | S status | No application fields | Existing shared handler matches this empty reply shape |

RVConnectionData's decoder at `0x8c8c70` reads a secure StationURL, a list of
special-protocol bytes and another StationURL. The login reply additionally
contains its final message String. A zero-length special-protocol list is a
server policy choice, not a recovered historical value.

`0x463700` issues account creation after bootstrap login; `0x463e60` performs
password change after logging into the selected user account. Account-management
jobs hash a new password, convert the 16-byte initial hash to hex, then dispatch
25/1 or 25/4. Check `0x8dc9c0`/`0x8dcb30` and the account reply decoder
`0x8dd660` when implementing the response/result-code paths. The 25/4 bool
decoder is particularly important: returning a four-byte success code would
misalign its reply.

The game passes creation parameter **0** at `0x463700`. Constructor `0x8dc3f0`
stores it at job `+0x90`; `0x8dc9c0` passes that field to the 25/1 writer.
The shared server defaults to **0x20**; Commandos explicitly supplies **0**
through the per-title signup policy alongside its bootstrap credentials.

Existing-account recovery is a UI/login path, not evidence of an unauthenticated
password-reset service. Account-delete strings survive in shared resources,
but this sweep found no corresponding outgoing delete request writer.

## PRUDP reuse and remaining transport check

`0x8c5320` applies the access password to stream-3 settings through `0x672970`.
That routine sums the password bytes modulo 256, yielding **`0xa6`** for
`7fas5`, with an optional formatted variant when the SDK suffix setting is
nonzero. The normal suffix is zero-initialized. `0x68d6f0` implements the same
wrapping u32-word checksum arithmetic as `prudp.Checksum`.

Packet input `0x682800` and output `0x682f40` match the existing 10-byte prefix,
SYN/CONNECT signature extension, DATA fragment byte, optional u16 size and
one-byte checksum. Their payload transformation depends on the stream setting's
codec pointer. That pointer's constructor `0x6728c0` initializes it to zero;
the packet code skips transformation when it is null. A first retail startup
capture must settle the effective stream-3 mode before selecting Quetzal's
payload codec. Neither TDU's RC4+LZO wrapper nor TTL's plain compression-selector
wrapper should be selected solely from the Quazal branding.

**Retail update, 2026-11-23:** the stock US signup attempt completed the auth
transport handshake, then sent 37 DATA payload bytes: selector 0 followed by a
valid 10/1 RMC record (body length 32, call ID 4, shipped signup principal).
The earlier raw-mode adapter rejected that record before dispatch. The default
is now `plain`, reusing the shared compression-selector codec. Subsequent stock
testing completed secure startup, account creation and named-account login on
both LAN PCs. Password-change UI and detailed recovery/result-code behavior
remain unverified; see the [run guide](running.md).
