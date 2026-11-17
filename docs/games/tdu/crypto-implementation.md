# Implementing the recovered TDU cryptography

Research provenance: these findings were produced in the sibling `tdu-server`
workspace. Runtime package paths below refer to Quetzal; `tools/`, private
`work/` evidence and Ghidra projects refer to that original research workspace.
Keep those private files there; Quetzal has no runtime dependency on them.
See [running.md](running.md) for current Quetzal operation.

The previously open ticket/challenge and native key-exchange formats are now
implemented as Go packages. This removes the unknown crypto formats from the
critical path to writing the server. The codecs are now integrated into a [runnable UDP backend](running.md) with
synthetic-client protocol tests. A successful retail login remains unvalidated. DNAS remains on the user's existing
revival server via local DNS.

## What can now be generated and verified

| Package | Implemented contract |
| --- | --- |
| `internal/quazalcrypto` | Signup/PID password derivation, RC4/HMAC-MD5 containers, opaque destination tickets, service-ticket envelopes, secure CONNECT challenge and nonce reply |
| `internal/games/tdu/proxycrypto` | Native client-hello verification, custom-group DH public exchange, signed server handshake messages, directional key derivation, AES-CTR/HMAC traffic and explicit sequence state |
| `internal/games/tdu/peercrypto` | Native client-generated EncKey tickets, descriptor/connection-ID/address checks, downloaded-key peer packet wrapper |

These are protocol codecs and state helpers. They intentionally do not bind
sockets, create accounts, accept an identity from an arbitrary endpoint, or
pretend that the game completed login.

## Keys we choose versus build-specific values

| Value | Who controls it / how to use it |
| --- | --- |
| Player signup hash | Store the decoded 16-byte initial hash; extend it by PID % 1024 MD5 rounds for that account's login key |
| Quazal backend principal key | The replacement server can choose and persist its own 16-byte key; ticket issuer and destination acceptor must use the same key |
| Quazal service-session key | Generate a fresh random 16-byte key for the service ticket; include it only in authenticated/encrypted containers |
| Quazal challenge nonce | Fresh client nonce; server replies with LE u32(nonce+1), inside a length-prefixed Buffer |
| Title access key / checksum seed | Stock build uses `yh64s`, initial seed `0xbe`; this is separate from account keys |
| Signup guest credential | Stock build's guest flow supplies its embedded credential; handle that bootstrap identity deliberately |
| Native handshake HMAC key | Build-specific decoded constants must match the stock client; ordinary certificates do not replace them |
| Native DH group and public format | Use the exact recovered group and encoding; a standard group merely named DH-1024 is different |
| Native DH private exponent | Generate fresh random bytes for each retained handshake state; keep the state across UDP retries |
| Native traffic keys/counters | Derive directional keys from DH; never restart counters while reusing the same keys |
| Downloaded `p2pkey` | The resource server supplies these 16 bytes; all peers in the intended population must receive compatible bytes |
| Native EncKey ticket key | Build-specific shared client key; ordinary peers generate these tickets locally |

No certificate is needed for these custom network formats. A replacement server
can own its opaque Quazal ticket key because the client forwards that ticket
unchanged; it cannot arbitrarily replace the native constants or public-key
encoding the stock client validates.

## Quazal account proof and secure connection

`InitialKey` performs 65000 MD5 operations; `ExtendInitial` adds PID % 1024.
`Seal` encrypts with a fresh RC4 state and appends HMAC-MD5 over ciphertext.
`Open` verifies the complete digest before returning plaintext.

The login validation blob has **no checked plaintext schema** in the retail
login validator: the client authenticates/decrypts it and discards its contents.
Its original server bytes are consequently neither recoverable from that code
nor required. Use real authenticated opaque validation data under the correct
account-derived key, not an unencrypted success flag.

`IssueServiceTicket` builds both service-ticket layers:

1. Destination plaintext: client PID (LE u32), session key (16 bytes).
2. Encrypt/authenticate that with the destination principal's key.
3. Client envelope: session key, destination PID, length-prefixed opaque ticket.
4. Encrypt/authenticate the envelope with the account's PID-derived key.

The client's secure CONNECT application payload contains two length-prefixed
Buffers: that opaque ticket, then an encrypted challenge containing client PID,
connection ID and nonce as three LE u32 values. `OpenConnect` handles both
containers and fixed layouts. `ConnectReply` returns the exact nonce+1 response.
The RPC and PRUDP framing outside these payloads is integrated in the runnable
server and exercised by real UDP tests.

The Go parser additionally requires the duplicate challenge PID to agree with
the authenticated ticket PID. The login-name request alone is not proof that the peer knows the account key.
Do not mark an endpoint authenticated merely because a proof blob was issued:
the secure ticket/challenge exchange demonstrates possession of the session key.
Ticket/account lookup, endpoint binding, expiry,
replay state and session-key generation remain the server's responsibilities;
the codec does not infer these from a source IP alone.

## Native proxy exchange

The initial raw master request carries a region/language value and build
revision 4392, not an authenticated player ID. The reply contains proxy IPv4
then **directory IPv4**. A zero second field also triggers the client's OLD
VERSION path; that check does not make arbitrary nonzero scalars valid directory
addresses. `0x377b80` transfers the second IPv4 into descriptor `0x5eb5cc`.
See [startup-handoff.md](startup-handoff.md) for the native-executed proof.
Neither address is a cipher key or part of the handshake transcript.

The proxy hello identifies a region and protocol version. A shared-build-key
handshake is not Quazal account proof. Current native session/presence records
are ephemeral and owned by the live transport generation; their connection-ID
claims do not authorize persistent account or statistics writes. The latter
use the ticket-proven Quazal context; see [title-services.md](title-services.md).

The game's DH group is labelled DH-1024, but its actual modulus is
`2^1036 - 0x0ff33ef9`, generator 4. The public record has six fixed header
bytes, a little-endian magnitude length and a big-endian unsigned magnitude,
inside a 140-byte payload. `DHPrivate` validates and preserves these distinctions.

`BeginServerHandshake` verifies command 1 and generates command 2 using retained
state. `Complete` verifies command 3, derives traffic keys and generates command
8. The transcript includes the client nonce and exact public-record bytes;
re-serializing an incoming padded public record before checking its MAC would
change the transcript. An exact command-3 retry returns the original finish.

`SealPacket` / `OpenPacket` implement the wire transform independently of replay
policy. `Sender` owns one monotonic transmit counter. `Receiver` uses a
four-packet sliding window and never moves its window backwards; that duplicate
policy is deliberately stricter than the retail last-accepted counter. Both
client-to-proxy and proxy-to-client directions have distinct keys.

The runnable server implements transport activation, keepalive, restricted local
service routing and optional read-only directory/resources around these codecs.
Native session/presence lifecycle and bounded Quazal introductions are now
integrated; see [sessionmaster.md](sessionmaster.md), [presence.md](presence.md)
and [peer-connectivity.md](peer-connectivity.md). Native account binding and
strict-NAT behavior remain unverified. The crypto helper grants no application
permissions and the backend provides no arbitrary UDP relay.

## Peer tickets and the downloaded key

The native client itself generates EncKey tickets in `0x151c40`; there is no
missing original central ticket server required to make ordinary peers issue
these messages. The ticket contains the intended receiving Quazal connection
ID (CID/RVCID) and the initiating peer's descriptor. It is 82 bytes of structured plaintext plus a
32-byte HMAC-SHA256, AES-256-CTR encrypted, followed by a clear 16-byte IV:
130 bytes total. `SealTicket`, `OpenTicket` and `ValidateBinding` cover that
format and its receiving connection-ID/address checks.

An EncKey's shared build key does not authenticate a central account grant.
Keep association/source/identity policy around it. Once associated, peer data
uses a separate repeating-key XOR wrapper and one-byte checksum based on the
downloaded `p2pkey`; it is not the proxy cipher or the EncKey cipher.
See [peer-security.md](peer-security.md) for the exact mixed-endian descriptor.

## Verification and its limits

Run the Go checks from Quetzal's root:

```sh
scripts/go test ./...
scripts/go vet ./...
```

Historical native-routine checks run from the sibling `tdu-server` research
workspace, which retains the private executable and tools:

```sh
python3 -m unittest discover -s tests -v
python3 tools/verify_crypto.py
```

The optional last command requires the installed `unicorn` and `cryptography`
Python packages and the user's documented executable. It executes retail:

- RC4 KSA/PRGA: 9 message-length cases, including reset between invocations.
- MD5: 13 cases covering padding/block boundaries.
- HMAC-MD5: 45 cases covering short/long keys and message boundaries.
- Peer sender `0x152c80`: 54 synthetic command/length combinations, retained as
  sanitized fixtures and compared byte-for-byte by the Go peer package.

The harness adapts selected 64/128-bit register saves and arithmetic to the
MIPS32 engine and substitutes only memory/SDK-accessor/I/O operations. It does
not substitute those tested crypto algorithms. Its scope and adaptations are
in `tools/ee_validation.py`. It is not a general EE emulator.

Other Go tests use independent hashlib/OpenSSL-compatible vectors for complete
ticket/proxy containers and handshake transcripts, malformed/tampered inputs,
key-direction separation, retry behavior and sequence boundaries. These are
independent checks of the recovered formats, not a claim that AES/DH wrappers
have all executed in a live retail connection.

## Next validation

Transport, account/ticket state, secure registration, native activation,
directory/resources, SessionMaster lifecycle, presence, bounded introductions
and authenticated title-data storage are implemented. Native-routine fixtures
also verify the initial directory handoff and resource jobs. Next validate a
fresh stock connection and generated configuration, then two-client discovery,
association and cruising. Completed races, regrouping and restrictive NAT
require separate checks; see [roadmap.md](roadmap.md).
