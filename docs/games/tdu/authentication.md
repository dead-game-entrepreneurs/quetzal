# Authentication, tickets and backend registration

Research provenance: these findings were produced in the sibling `tdu-server`
workspace. Runtime package paths below refer to Quetzal; `tools/`, private
`work/` evidence and Ghidra projects refer to that original research workspace.
Keep those private files there; Quetzal has no runtime dependency on them.
See [running.md](running.md) for current Quetzal operation.

Target: US PS2 `SLUS-214.90`, SHA-256 recorded in [build.json](build.json).
All addresses below are main ELF virtual addresses. Static findings now back
a runnable auth/secure-RDV service in `internal/protocol/quazal` and offline crypto in
`internal/quazalcrypto`. Handler integration tests exercise real ticket proof,
signup, account login, registration and bootstrap lookup. No retail login has
been validated by this project.

## The login sequence

The account system has several distinct exchanges. Completing the first UDP
connection is only the beginning of going online.

```mermaid
sequenceDiagram
    participant C as PS2 client
    participant A as Authentication service
    participant S as Secure rendezvous service
    C->>A: PRUDP connection to configured authentication endpoint
    C->>A: Protocol 10 / method 1 or 2: login name
    A-->>C: Result, PID, encrypted validation blob, connection data, message
    Note over C: Derive key from password and returned PID; validate blob
    C->>A: Protocol 10 / method 3: request destination service ticket
    A-->>C: Result and encrypted service-ticket envelope
    Note over C: Recover session key, destination PID and opaque service ticket
    C->>S: Secure endpoint connection using ticket and challenge material
    S-->>C: Validated connection response
    C->>S: Protocol 11 / method 1 or 4: register client StationURLs
    S-->>C: Result, connection ID and StationURL
    Note over C: Backend login can complete; game-specific services follow
```

The request order is supported by the backend login job, ticket-manager job and
secure registration calls. A retail trace must establish which optional branches
are actually selected by each front-end action.

The account/login setup uses `tdulive.quazal.net:30200` (`0x1e1060`, `0x1e2e70`).
Authentication URL construction at `0x1e9070` uses stream `3`, session ID `1`,
type `2`. After processing authentication, `0x1e95d0` prefers a returned secure
URL when its required properties exist; otherwise it constructs a `prudps:/`
URL from the original host and an offset from the auth port. The fallback's
selector is initialized to `2` at `0x56f188`, yielding a static initial candidate
of port `30201` and URL `PID=2`. That is an initializer/fallback observation,
not proof that every retail connection always uses that destination. Returned
connection data and runtime configuration can choose another URL.

## RPC envelope and primitive types

Every RMC Message begins with **u32 LE byte count excluding those four bytes**.
`0x21a960` reserves the four-byte header, `0x21aa70` returns its size, and
`0x21aa80` updates the count before transmission. The following tables describe
the bytes after this mandatory prefix. PRUDP datagram headers, stream
fragmentation, compression and encryption remain separate transport concerns.

| Request field | Encoding | Evidence |
| --- | --- | --- |
| Protocol/direction | u8 `protocolID | 0x80` | `0x239ec0`, `0x239f10` |
| Call ID | u32 LE | `0x237750`, `0x237860` |
| Method ID | u32 LE | `0x2378c0` |
| Method arguments | Method-specific fields | Request serializers below |

The protocol byte uses bit 7 for request direction. The reply broker flips that
bit with `0x239f80`; `0x238e50` reads the byte and dispatches requests or replies.

A successful reply is:

| Field | Encoding |
| --- | --- |
| Protocol/direction | u8 protocol ID with request bit clear |
| RPC success | u8, nonzero for success; the writer uses `1` |
| Call ID | u32 LE |
| Reply method ID | u32 LE, observed as method ID OR `0x8000` |
| Application result/output | Method-specific fields |

An RPC-level failure instead has protocol byte, zero success byte, u32 error
code, then u32 call ID. It does not carry the ordinary successful application
output. The status writer/reader are `0x2375c0` / `0x237670`; `0x2377c0` proves
that the call ID follows the status and any error code. Reply broker `0x239560`
adds the protocol byte. `0x238420` dispatches successful replies to the concrete
client decoder after resolving the call ID.

Do not confuse that outer success flag with a method's application-level
`qResult`: login can arrive as a successfully delivered RPC whose application
result rejects the account.

| Primitive | Encoding and evidence |
| --- | --- |
| u32 | Direct four-byte copy; PS2 build is little endian (`0x2362f0`, `0x236320`) |
| bool | One byte in the inspected protocol readers |
| String | u16 LE byte count, then bytes; ordinary non-null strings include terminating NUL (`0x23b0c0`, `0x23b170`) |
| Null String representation | Zero u16 length; constant at `0x56f850` |
| Buffer | u32 LE byte count, then that many bytes (`0x235ea0` and generated readers) |
| StationURL | String holding a URL (`0x23b250`, `0x23b2b0`) |
| qResult | One u32 on wire; the C++ wrapper occupies more memory (`0x23b1f0`) |

The scalar-copy routines do not add DemonWare type tags or align each argument
to a four-byte boundary. Treat fields as a packed byte stream.

## Protocol 10: ticket granting

The protocol ID is set to decimal `10` by `0x1f46a0` through `0x2374b0`.
`0x1f3190` selects ordinary login or the extra-object login path.

| Method | Request | Successful application output | Main evidence |
| --- | --- | --- | --- |
| 1: login | String login name | qResult, u32 PID, Buffer, RVConnectionData, String | Request `0x1f0e10`; decoder `0x1f1350`, case `0x8001` |
| 2: login with extra object | String login name, virtual AnyData serialization | Same fields as method 1 | `0x1f1050`; decoder case `0x8002` |
| 3: acquire ticket | u32 source/client PID, u32 destination PID | qResult, Buffer | `0x1f0c70`, `0x1f3940`; decoder case `0x8003` |
| 4 | Request not mapped in this pass | u32 | Decoder case `0x8004` |
| 5 | Request not mapped in this pass | String | Decoder case `0x8005` |

The ordinary login request contains the login name. The inspected method 1
serializer does not send the password as an argument. The password is used
locally to derive a key and validate/decrypt the server-returned buffer.
The method 2 extra object uses the registry at `0x56f918`; its concrete title
schema and retail branch selection still require recovery.

### RVConnectionData

The generated reader `0x1f0b60` consumes:

1. StationURL String.
2. u32 element count, then that many u8 elements.
3. Another StationURL String.

The backend login job uses returned data to choose subsequent connections.
The second URL is used by the explicitly named special-connection stage
`0x1e99d0`; an empty URL takes its skip path. The current service returns one
regular secure StationURL, zero byte-list elements and an empty special URL.
No optional special-protocol route is offered. The byte list contains protocol
IDs routed through the special endpoint: backend setters `0x1ede70`,
`0x1ede80` and `0x1ede90` store the regular endpoint, special endpoint and
list respectively. The selector `0x1ee010` chooses the authentication endpoint
for protocol 10, the special endpoint for a listed protocol ID, and the regular
endpoint otherwise. An empty list therefore routes protocols 11, 25 and 104
through the regular secure connection. Keep the list and both URL fields
explicit because a future service may supply a special route. This routing is
verified statically; the current empty-list response still needs a stock-client
integration test.

## Password derivation and validation

The concrete key derivation object is `Quazal::MD5KeyDerivation`, vtable
`0x560f70`. Its constructor `0x1ebd10` sets base iterations `65000` and modulus
`1024`. Static initializer `0x555210` constructs the default instance at
`0x5aa9e8`; pointer `0x56f1d0` selects it.

For login, `0x1f3490` passes the returned PID and the password to the derivation
path. The concrete asynchronous implementation `0x1ec0a0` chooses:

`iterations = 65000 + (PID % 1024)`

`0x232570` copies the password bytes using `strlen`, excluding a terminating
NUL. The digest loop (`0x2329f0`, with synchronous equivalent `0x232160`) starts
with those bytes and repeatedly replaces the working bytes with their MD5
binary digest. It performs exactly the requested number of iterations; the
final key is the raw 16-byte digest, not its hexadecimal spelling.

The login validator `0x1f35d0` installs that derived key in the authentication
client's crypto object and validates/decrypts the returned encrypted buffer.
It discards the plaintext without reading any fields. There is therefore no
required validation-plaintext schema in this client path: acceptance proves
knowledge of the password-derived key through the container MAC. The original
server's choice of validation bytes cannot be recovered from an unused value;
a replacement can supply fresh opaque validation bytes under the correct key.
This is separate from the later service-ticket envelope, whose plaintext is parsed.
An empty plaintext also reaches the same crypto success path statically, but the
implementation does not depend on choosing that edge case.

The title's shared transport access key, default transport cipher key and this
per-account password-derived key are separate inputs with separate purposes.
The native SDK initializer `0x1e0630` selects the title access-key String
`yh64s` at `0x51b2f1`; wrapper `0x1e75b0` reaches setter `0x1ecce0`, which
copies it to the 128-byte buffer at `0x5aaa00` with a maximum count of `0x7f`.
Stream setup `0x1ec870` supplies that text to checksum-seed setter `0x219e80`.
Its plain byte sum is `0xbe` modulo 256, before any optional formatted variant.
The other embedded text `h7fyctiuucf` at `0x51b311` serves as a signup guest
credential in the native account-creation path; it is not this title access key.

### Ticket encryption and authentication

AuthenticationClient construction `0x1f43f0` allocates `0x1eb7b0`, which combines
an RC4 object at its base with an HMAC object at offset `0x22c`.
`0x1eb870` applies the key to both parts. RC4's generic constructor supplies a
default key, but ticket processing replaces it; that default is not the user's
password key.

`0x1eb8c0` first verifies/removes a trailing keyed digest and then invokes the
cipher's decrypt operation. Related explicit helper `0x1ebae0` constructs the
same primitives, sets a caller-supplied key, validates the keyed digest and only
then decrypts the ciphertext. HMAC setup `0x234970` implements a 64-byte block
with `0x36` / `0x5c` pads, hashing long keys first; digest routine `0x234a90`
performs the inner and outer MD5 operations and emits 16 bytes. Thus the
observed container is RC4 ciphertext followed by a 16-byte HMAC-MD5 over the
ciphertext, using the selected key for those operations.

The RC4 transform `0x231820` calls reset `0x2319b0` before each operation.
That reset zeroes the two PRGA indices and copies the 256-byte saved key-scheduled
S-box at context `+0x24` into the working box at `+0x124`. Each authenticated
container therefore starts at keystream offset zero; it does not share a running
RC4 cursor with the next ticket or challenge.

The Go implementation below has independent synthetic ARC4/HMAC vectors and
tampering tests. Retail capture validation and the enclosing PRUDP connection
state remain separate from these cryptographic primitive checks.

## Service-ticket acquisition and secure endpoint authentication

Ticket acquisition job `0x1f3940` first checks its cache. On a miss it emits
protocol 10, method 3, with two u32 values through `0x1f0c70`. Its successful
response processor `0x1f3b50` authenticates/decrypts the returned Buffer using
the authentication client's derived-key object, then reads this plaintext:

| Offset / order | Field |
| --- | --- |
| 0 | 16 raw bytes of session key |
| 16 | u32 destination/server PID |
| 20 | u32 opaque-ticket byte count |
| 24 | Opaque-ticket bytes |

That processor constructs a cached Ticket through `0x1f2110`, preserving the
opaque ticket, client PID, server PID and session key. It is a different
plaintext structure from a ticket received and validated by an endpoint.

### Opaque destination ticket and key scope

The inbound Kerberos helper at `0x1f3f90`, called by secure endpoint handler
`0x1f7500`, decrypts the first Buffer using the receiving AuthenticationClient's
crypto object at `+0x28`. That object carries that receiving principal's key.
The opaque ticket is therefore encrypted/authenticated for the destination,
not for the requesting client. For a backend service the ticket issuer and
accepting service must share the configured service-principal key. For a player
as destination, use that player's stored PID-specific password key. This scope
comes from the receiving key lookup; the original backend's particular secret
value is neither embedded here nor required for a replacement server.

`0x1f2250` reads the destination ticket's plaintext as:

| Offset | Field |
| --- | --- |
| 0 | u32 remote/client principal ID |
| 4 | 16 raw session-key bytes |

The PID identification is supported by the complete use chain: the reader
stores its first u32 in Ticket `+0x20`; `0x1f7500` passes that value to endpoint
principal setter `0x213ca0`, which stores it at endpoint `+0x18` and updates
principal lookup registration. No destination PID, expiry or lifetime field is
read from these 20 bytes. Server-issued tickets can use this exact layout;
lifetime and replay enforcement belong in server session state.

### Exact outbound CONNECT and reply

Constructor `0x1f3e30`, reached from connect-job stage `0x1f8140`, writes two
u32-length-prefixed Buffers:

1. The opaque destination ticket, forwarded unchanged.
2. A sealed challenge, using the service-ticket session key. Before RC4/HMAC,
   this challenge contains exactly three LE u32 values:

| Challenge offset | Field and source |
| --- | --- |
| 0 | Local/client PID, AuthenticationClient `+0x30` |
| 4 | Local connection ID from its backend context, or zero when absent |
| 8 | Fresh challenge value returned by `0x200c80(-1)` |

The explicit helper `0x1eb970` creates fresh RC4 and HMAC objects, installs the
session key, encrypts the 12 bytes and appends the ciphertext MAC. Together with
the 36-byte sealed destination ticket, this produces a 72-byte CONNECT
application payload before PRUDP headers: `4+36 + 4+(12+16)`.

The receiver authenticates the destination ticket first, then uses its recovered
session key to open the second Buffer. It installs the ticket PID as the peer
principal; the challenge's connection ID becomes the endpoint connection-ID
value through `0x213c20`. It does not itself compare the challenge's duplicate
PID to the ticket PID. The new server helper makes that extra consistency check
explicit and rejects a mismatch.

The successful reply application payload is a Buffer containing one LE u32,
`nonce + 1` with 32-bit wrap: `04 00 00 00` followed by that value. It is not an
additional encrypted challenge container. Connect-job completion `0x1f86a0`
calls `0x1f42f0`, which reads this Buffer and compares its first u32 to the
stored nonce plus one. PRUDP transport encoding still applies around the reply.

The Go parser requires exact 20-byte ticket and 12-byte challenge plaintexts,
exact Buffer boundaries and an exact eight-byte reply. The client reader is
more permissive about trailing bytes; exactness is deliberate server policy,
not a claim that the client rejects every excess byte.

## Protocol 11: secure connection and URL registration

SecureConnectionClient constructor `0x1f5e50` sets protocol ID decimal `11`.
Backend stage `0x1e9ca0` reaches registration through `0x1f60a0` after the secure
endpoint connection. `0x1f5fd0` gathers local StationURLs and assigns the
configured stream-related property before serialization.

| Method | Request fields | Successful application output | Evidence |
| --- | --- | --- | --- |
| 1: register URLs | u32 count, then count StationURL Strings | qResult, u32 connection ID, StationURL | `0x1f48c0`, decoder `0x1f51a0` case `0x8001` |
| 2 | Two u32 values | bool, generated list/object of 12-byte in-memory elements | `0x1f4b20`, decoder case `0x8002` |
| 3 | Request not mapped | bool, StationURL collection | Decoder case `0x8003` |
| 4: registration with extra object | Same URL list, then virtual AnyData serialization | qResult, u32 connection ID, StationURL | `0x1f4d20`, decoder case `0x8004` |

The default registration wrapper chooses method 1 when no optional extra object
is supplied, otherwise method 4. The returned connection ID and StationURL
need to be preserved: this is where the server learns the client's connection
address data used by later rendezvous and NAT traversal services.

## Protocol 25: account creation

AccountManagementClient constructor `0x23c610` sets protocol ID decimal `25`.
This account signup flow has its own temporary backend login. The creation job
starts with `LoginGuest` (`0x23d9c0`), using the literal login name `guest` at
`0x56f868` and the configured guest credential. The native title path at
`0x1e1060` passes `h7fyctiuucf` from `0x51b311` into helper `0x23ca50`;
creation-job constructor `0x23d550` stores that credential at job offset `0x60`,
and `0x23d9c0` passes it as the guest login password. After that connection succeeds,
`CreateAccount` stage `0x23db50` starts the management job; the job later logs
the guest connection out. This is separate from authenticating the requested
new account.

`JobManageAccount::PasswordInitialHash` (`0x23cf20`) calls the initial-key path
`0x1ebcb0`. Its concrete virtual slot reaches `0x1ec1b0`, which requests exactly
`65000` MD5 iterations over the password bytes. The result is formatted as
lowercase hexadecimal by `0x2368e0`, via `0x236870`, before signup serialization.

Protocol 25, method 1 (`0x23e030`) writes:

1. String account/login name.
2. String containing the lowercase hexadecimal initial hash.
3. One u32 account parameter; retain it until its semantics are confirmed.
4. String account/contact parameter; retain it until the front-end mapping is confirmed.

Its successful application reply is qResult only (`0x23e7e0`, case `0x8001`).
The client does not send the raw password in that inspected signup serializer.
The server-side relationship between the stored initial hash and the PID-based
login hash must be implemented deliberately and tested; it follows that the
additional `PID % 1024` rounds can be applied to the binary initial digest,
but that server storage policy is an inference from the recovered algorithm.

The decoder also exposes 20 account-management method slots, covering richer
account information and operations. Most have not been traced to title menu
calls in this pass. Do not implement them all as successful no-ops.

## Implementation implications and unresolved work

The first useful server milestone can be defined precisely: handle the title's
PRUDP transport, accept protocol 10 login, generate a valid derived-key encrypted
validation result, issue/cache service tickets correctly, complete secure
endpoint authentication, then accept protocol 11 URL registration. Reaching the
online world will additionally require the game-specific service calls that
follow this SDK login job.

Still required before claiming a working login:

- Confirm actual retail method 1/2 and registration method 1/4 selection.
- Recover the concrete optional AnyData types selected by the title.
- Choose and persist the backend service-principal key, bind ticket issuance
  to authenticated source/destination PIDs, and implement validity/replay policy
  in the eventual server. The ticket bytes do not encode their own lifetime.
- Capture valid/invalid password and duplicate-account behavior, rather than
  choosing arbitrary qResult or RPC error constants.
- Confirm timeout, retry, disconnect and re-login state transitions on a stock
  client, including refreshing cached service tickets.
- Validate bounded parsers against oversized counts, strings and buffers; the
  client code's direct copies are evidence of format, not validation policy for
  a new server.

## Usable Go crypto and codecs

`internal/quazalcrypto` contains standard-library implementations of the recovered
formats. It opens no sockets and accepts no fake accounts. Its public entry points
are:

| API | Purpose |
| --- | --- |
| `InitialKey`, `PasswordKey`, `ExtendInitial` | Signup and PID-specific derivation using raw 16-byte `Key` values |
| `Seal`, `Open` | Fresh-state RC4 plus ciphertext HMAC-MD5; authenticate before decrypting |
| `DestinationTicket`, `ServiceTicketEnvelope` | Fixed destination-ticket and length-bounded outer-envelope codecs |
| `IssueServiceTicket`, `OpenServiceTicket` | Apply the destination key to the opaque layer and client key to the envelope |
| `Challenge`, `ConnectRequest` | Exact three-u32 challenge and two-Buffer CONNECT codecs |
| `BuildConnect`, `OpenConnect` | Create/validate both authenticated CONNECT layers; bind duplicate PID |
| `ConnectReply`, `VerifyConnectReply` | Length-prefixed nonce increment, including uint32 wrap |

Callers own random session-key/nonce generation, source/destination key lookup,
credential policy, limits on incoming frame sizes, ticket lifetime and replay
state. `Open` returns no plaintext on MAC failure and uses constant-time MAC
comparison. Variable-length decoders validate declared sizes against the actual
input before slicing or allocating and reject trailing bytes.

The seven unit tests include independent Python `hashlib`, `hmac` and
`cryptography` ARC4 vectors, PID modulus boundaries, every-byte ciphertext/tag
tampering, wrong-key rejection, separate client/destination keys, a complete
nested ticket/CONNECT fixture, nonce wrap and malformed/truncated lengths.
All fixture keys and payloads are synthetic.

```sh
scripts/go test -v ./internal/quazalcrypto
scripts/go vet ./internal/quazalcrypto
scripts/go test -run '^$' -fuzz '^FuzzConnectAndTicketBounds$' -fuzztime=3s -parallel=1 ./internal/quazalcrypto
```

## Runnable application service

`internal/protocol/quazal` implements two `prudp.Handler` roles sharing one `Store`.
The auth endpoint accepts the empty authentication CONNECT security payload;
the secure endpoint requires the complete authenticated ticket/challenge.
`New(Options)` does not bind sockets; the command/runtime owns the UDP listeners.

| Role | Supported call | Result / authorization |
| --- | --- | --- |
| Auth | Protocol 10, method 1 | Select guest or existing account; return its PID, genuine password-key validation proof and regular secure URL |
| Auth | Protocol 10, method 3 | Issue a destination ticket only for the same connection's selected source PID and configured backend principal |
| Secure | Protocol 11, method 1 | Register bounded client StationURLs after ticket proof; return a server connection ID and the observed UDP endpoint URL |
| Secure | Protocol 25, method 1 | Create an account only from a registered, proven guest connection; require the recovered signup parameter `0x20` |
| Secure | Protocol 104, method 1 | Return configured native bootstrap IPv4 only after ticket proof and URL registration; require no arguments |

Methods 2/4 carrying optional AnyData, other account methods and all unknown
protocols return an explicit RPC failure and a structured rejection log. The
current generic failure number `0x80010002` is a deliberate server policy;
its finer retail UI error interpretation has not been established. Successful
application `qResult` is `0x00010001`, confirmed against the client's high-bit
success predicate at `0x207360` and constant data such as `0x56f224`.

A name-only auth request is provisional account selection, not proof that its
sender knows the password. The encrypted validation blob and ticket envelope
are opened by the actual account key. The secure CONNECT then proves knowledge
of the session key from that envelope before a peer receives protected service
access. The service rejects mismatched source PIDs, unknown/expired tickets,
modified containers and cross-peer reuse of a CONNECT nonce. Retries on the
same transport peer are idempotent. Cached RPC replies likewise allow an exact
retransmission while rejecting a changed request using the same call ID.

`OpenStore(path)` persists account names, allocated PIDs and hexadecimal initial
hashes in versioned JSON. Updates use a private temporary file, file sync and
atomic rename; mode is 0600. A failed write does not publish the account in
memory. Existing nonprivate files, symlinks, malformed hashes, duplicate
identities and unknown JSON fields are rejected. Account-name lookup is ASCII
case-insensitive, with 3–15 printable nonspace bytes. These name constraints
and allocation starting at PID 1000 are explicit server policies. Guest PID
100 is reserved; the configured backend PID is separately reserved.

The signup contact field is decoded to validate the wire record and is not
persisted. The server never logs password bytes, initial hashes, session keys
or container plaintext. Its account hashes remain credential-equivalent data
inside the private store. The native bootstrap scalar is serialized as the
little-endian numeric value of the big-endian IPv4 address; the caller's
`0x13aca0` conversion then supplies the native network-order field.

Issued session keys and accepted CONNECT nonces are runtime state. The default
ticket admission lifetime is 15 minutes; a restart requires obtaining fresh
tickets. This does not terminate an already proven connection solely because
its original admission ticket later expires. Issued-ticket capacity, per-ticket
connection count, message lengths, URL counts and cached calls are bounded.
There is no password bypass or unconditional-success path for an unknown task.

The service tests complete guest secure login, URL registration, account signup,
private persistence/reload, ordinary account login, ticket issuance, secure
challenge and the bootstrap query using real `quazalcrypto` containers. They also
cover wrong passwords, PID spoofing, expiry, cross-peer replay, tampering,
unsupported calls, malformed prefixes and atomic-write failure. Vet passes; a
bounded three-second `FuzzRMCBoundaries` run completed 80,913 inputs without a
failure. These are executable application checks, not stock-PS2 interoperability
claims.

```sh
scripts/go test -v ./internal/protocol/quazal
scripts/go vet ./internal/protocol/quazal
```

## Evidence and reproduction

The isolated research copy is under ignored `work/authentication/ps2/` and uses
its own `tdu.gpr` / `tdu.rep`. No shared project database was modified. Ghidra
queries used `tools/research.py` with `--work` and `--log`.

Useful retained logs in that directory:

| Log | Main contents |
| --- | --- |
| `ticket-initial.log` | Login requests, validation and URL selection |
| `rpc-initial.log` | Protocol bit, raw scalar and String serialization |
| `auth-codecs.log` | Protocol IDs, qResult and RVConnectionData readers |
| `auth-crypto.log` | Default derivation constants, hash loop and ticket helpers |
| `auth-envelope.log` | Composite auth cipher, HMAC and protocol broker |
| `auth-flow-final.log` | RPC status/call ordering, async hash loop and secure handler |
| `auth-registration.log` | RPC direction handling and registration stage |
| `auth-register-wire.log` | Protocol 11 request field order and HMAC pads |
| `auth-create-account.log` | Protocol 25 registration and job setup |
| `auth-signup-wire.log` | Guest login and account-creation stage |
| `auth-signup-codec.log` | Signup method 1 and service-ticket plaintext reader |
| `auth-ticket-acquisition.log` | Ticket acquisition job and Buffer codec |
| `auth-last.log` | AcquireTicket request and lowercase hex encoding |
| `auth-signup-final.log` | Initial password hash stage and account reply decoder |
| `accesskey-callers.log`, `accesskey-chain.log` | Title access-key setter and native initializer |
| `secure-connect-exact.log`, `secure-connect-validation.log` | Outbound challenge, reply stage and identity setters |
| `secure-connect-final.log`, `rc4-reset-copy.log` | Exact nonce comparison, ticket reader and per-operation RC4 reset |
| `runtime-rmc-prefix.log`, `runtime-rmc-layout.log` | Required RMC length prefix and actual qResult success checks |
| `runtime-rv-fields.log` | Regular/special connection and login-completion paths |

For example:

Run these historical research commands from the sibling `tdu-server` root:

```sh
python3 tools/research.py --work "$PWD/work/authentication/ps2" \
  --log "$PWD/work/authentication/ps2/recheck.log" decompile \
  0x1f0c70 0x1f0e10 0x1f1350 0x1ebd10 0x2329f0 0x1f3b50 0x1f48c0
```
