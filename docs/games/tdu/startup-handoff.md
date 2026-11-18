# Native startup handoff

The US SLUS-214.90 client obtains its first native directory address from the
**second IPv4 address in the UDP 8124 master reply**. This resolves the previously
unmapped initial directory handoff. It is verified statically and by limited
execution of retail routines; a complete stock-client online session remains
untested. Binary identity and mapping are in [analysis.md](analysis.md).

## Quazal bootstrap address byte order

Protocol 104 / method 1 returns **four raw IPv4 bytes in network order**.
For `192.0.2.10`, the payload is `c000020a`, not the little-endian integer
encoding `0a0200c0`. The ordinary RMC envelope remains little-endian.

Retail response dispatcher `0x247b10` calls raw reader `0x236320` /
`0x236230` and copies the bytes into the output slot without scalar decoding.
Getter `0x1e2c20`, online-entry conversion `0x13aca0`, connection constructor
`0x1553b0` and UDP sender `0x142c60` carry that value to the socket address.
The earlier scalar encoding reverses the actual destination: a local server at
`192.168.2.141` is advertised to the native connection as `141.2.168.192`.
This explains successful account creation/registration followed by a timeout
without a master request arriving at the intended server.

`internal/games/tdu/testdata/native-bootstrap-address.json` records execution
through those retail routines to the socket-send boundary, using two synthetic
addresses. The incorrect reversed encodings demonstrably send to different
hosts. The UDP integration regression compares both advertised addresses with
these independently consumed bytes. Framework substitutions and reproduction
are documented beside the fixture. This establishes the address format, not a
successful retail online session.

## Master response and directory installation

The eight-byte response contains two four-byte IPv4 addresses in network order:

| Wire offset | Meaning | Receiving field |
| --- | --- | --- |
| 0 | Assigned proxy IPv4; client uses UDP 8122 | Native connection `+0x24` |
| 4 | Native service-directory IPv4; logical port initially 9000 | Native connection `+0x20` |

The state-1 branch of `0x154410` copies these bytes directly into the two fields.
It rejects a zero second address through its `OLD VERSION` path, then starts the
proxy handshake. The second address is not part of that handshake's crypto
transcript. Its later consumer is the online-entry routine `0x377b80`:

1. Once the native connection reaches state 6, `0x377ea4` calls `0x13ac50`
   on the raw word at connection `+0x20`.
2. `0x377eb4` selects global `0x5eb5c0` and writes descriptor kind 1 there.
3. The delay-slot store at **`0x377ec4`** writes the converted address at
   `0x5eb5cc`, the IPv4 field inside descriptor `0x5eb564`.
4. The routine clears manager status `0x5eb560` and advances UI state to 6.
   Periodic refresh `0x4610e0` can now issue the five-slot directory request.

The engine's integer representation of `203.0.113.77` is `0xcb00714d`. The raw
PS2 little-endian word containing wire bytes `cb 00 71 4d` is `0x4d7100cb`;
`0x13ac50` swaps it to the engine representation. The shared endian flag at
`0x4e5fb0` starts at -1 and is initialized to 1 by stream initialization
`0x13a9c0` / static initializer `0x551400`.

This store uses a computed base plus offset rather than a literal pointer to
`0x5eb5cc`; an absolute-pointer scan alone misses it. The earlier observation
that only INI overrides can populate the directory was incomplete. The shipped
INI's commented development `ORB` settings are optional overrides, not the
normal source of the first directory address.

## Offline native check

The original research workspace retains:

- `work/authentication/ps2/startup-master-directory.log`: retail parser,
  established-state assignment and endian conversion evidence.
- `work/authentication/ps2/startup-endianness.log`: runtime endian initialization.
- `work/authentication/verify_startup_handoff.py`: limited Unicorn execution.
- `work/authentication/ps2/startup-handoff-native.json`: sanitized result.

The fixture sends `c6336407cb00714d`, representing proxy `198.51.100.7` and
directory `203.0.113.77`. Actual routine `0x154410` stores both raw fields,
advances state 1 to 2 and emits command 1. A separate fresh emulator executes
`0x377e94..0x377ee0`, including the actual endian routine and branch delay-slot
store. It produces directory integer `0xcb00714d` and descriptor kind 1.
Only memory copying, random-byte generation and outgoing datagram transmission
are mocked. The harness uses the documented scoped EE-to-MIPS32 save-register
normalization and sets the statically verified endian flag. It does not execute
the full game, network stack, UI or retail login session.

Quetzal's `proxycrypto.MasterReply.DirectoryIP` now expresses this field as an
IPv4 address. `nativeproxy.Options.DirectoryIP` defaults to the public server
address. Unsupported builds receive zero proxy and directory addresses. Codec
and real-UDP tests cover distinct proxy/directory addresses, network byte order,
invalid overrides and the zero-address failure response. Sending scalar 1 here
would instead configure directory address `0.0.0.1` and obstruct startup.

## Activation callbacks and account identity

The ordinary stock path is command 29, empty command-30 reply, native state 5,
then the timer transition to state 6 in `0x153fe0`. Command 30 does not consume a
directory-address payload. The online routine then performs the master-field
handoff described above.

`0x377b80` installs these native callback slots:

| Object slot | Callback | Inspected role |
| --- | --- | --- |
| `+0x48` | `0x3a1350` | Text/notification handling |
| `+0x4c` | `0x3a12f0` | Network configuration parser and keepalive timing |
| `+0x58` | `0x3a14b0` | Display server message, receive command 100 |
| `+0x5c` | `0x3a1520` | Store message/state, receive command 101 |
| `+0x60` | `0x3a12d0` | Request netconfig.xml, receive command 102 |

Receive commands 32 and 33 use **different slots**, `+0x50` and `+0x54`,
respectively, while both set state 6. Command 32 also sets byte `+0x96`.
The inspected constructor clears `+0x50`; it does not explicitly initialize
`+0x54`. Neither is installed by this online-entry routine. Their general
success/failure semantics are therefore not established by state 6 alone;
ordinary activation should use the proven command-30 path.

The native hello's identity-looking scalar at `+0x98` is the region/language
value, initialized from the `us` bytes by `0x377b80` and constructor `0x1553b0`.
Command 38 retransmits that same region. It supplies no password, Quazal session
key or proof of a principal ID. Master assignment and the shared-build-key native
handshake likewise establish no account binding. A later descriptor may carry a
claimed Quazal PID, but this is not equivalent to a ticket-proven Quazal peer.
Native ephemeral session ownership must remain attached to the server's live
connection generation; account/progression writes require separate proof.
The exact Quazal-to-native account binding remains unproven.
