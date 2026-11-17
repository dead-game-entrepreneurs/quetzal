# TDU challenge files and reported statistics

`internal/games/tdu/titledata` implements the linked US PS2 title operations in
Quazal protocols 102 and 103. These run through the existing shared, reliable,
ticket-authenticated Quazal service. A registered named account is required;
native transport identity and self-reported native PIDs cannot write this store.
No car simulation, money grant or single-player mission script is run here.

## Recovered records and methods

All fields use the little-endian Quazal SDK format. Strings are u16 byte count
including a NUL; byte buffers are u32 length plus bytes. Lists begin with a u32
count. Concrete records have no extra AnyData/type wrapper. Application outputs
below do not prepend a qResult: the usual outer RMC success/error envelope remains.

| Protocol/method | Arguments | Implemented application output |
| --- | --- | --- |
| 102/1 | Category u32, name, description, creator, Buffer | One u32; replacement policy returns the stored file ID |
| 102/2 | Category u32, file ID u64 | Bool byte and Buffer; missing/category mismatch returns false plus empty Buffer |
| 102/3 | Category u32 | Actual matching ChallengeFileStats list, bounded to twenty |
| 102/4 | Requested count u32, offset u32, filter bool, name string | Stored ChallengeFileStats list; implemented filter is false plus case-insensitive name substring |
| 103/1 | GameStats | Bool byte, true only after durable storage |
| 103/2 | Selector u32, offset u32, count u32 | GameStats page and total u32 |
| 103/4 | Account name | Matching GameStats list, population u32 and six ranking positions |
| 103/6 | EventStats | Bool byte, true only after durable storage |
| 103/7 | Event ID u64, selector u32, offset u32, count u32 | EventStats page and total u32 |
| 103/10 | List of event IDs u64, account name | Existing matching EventStats and corresponding ranking positions |

Request builders are `0x240460`, `0x240600`, `0x240790` for linked challenge
operations, and `0x242360`, `0x242540`, `0x242710`, `0x242860`, `0x2429f0`,
`0x242ba0` for the linked statistics operations. Response dispatchers are
`0x240060` and `0x2417d0`. Bool reader `0x236520` consumes exactly one byte.
Challenge upload has exactly one u32 Buffer length: the PS2 builder writes its
size then raw bytes, not a second nested length. The public PC reference is not
used to override these PS2 widths.

| Record | Exact serialized order |
| --- | --- |
| EventStats | u64 event ID, account-name string, four u32 words |
| GameStats | Account-name string, three u32 words, u64 bits, u32 counter, two u64 bit fields |
| ChallengeFileStats | u64 ID, u32 category, name/description/creator strings, seven u32 metadata words, one byte flag |

Writers/readers `0x241360`, `0x241410`, `0x241610`, `0x2416b0` establish event
records; `0x24a9d0` and the GameStats DDL reader establish game records.
Challenge metadata reader/writer `0x23fe20`/`0x23ff40` proves **seven** metadata
words. Complete semantics of every numeric field and metadata flag are not
established. The implementation preserves positional values and 64-bit patterns
rather than assigning invented game rules to opaque data.

Submission wrappers `0x243bd0`/`0x243c90` populate the reported account name from
login-name getter `0x1e2420`. Reports/uploads must match that authenticated
account, case-insensitively; stored names use its actual account spelling.
UI `0x371860` requests game selectors 2..7 from `0x500ef0`; `0x371b00` selects
event selector 1 or 4 and requests at most twenty displayed rows. The challenge
browser also requests batches of 100, but its twenty-row local presentation is
respected by bounding returned pages to twenty.

## Explicit replacement policies

These are deliberate server policies, not claims to reproduce unknown original
ranking formulas:

- Store the latest reported profile and event snapshot for the exact authenticated
  PID, with separate event IDs. Duplicate identical challenge uploads by the same
  account return the existing ID and do not grow storage.
- Global game selectors 2..7 sort their corresponding numeric fields descending;
  known compiled missing-value `0xffffffff` sentinels and NaNs do not outrank
  actual reports. Ties use account name for deterministic order.
- Event selector 1 sorts metric zero ascending; selector 4 sorts metric three
  descending. Original ranking/scoring/anti-cheat formulas remain unverified.
- Challenge metadata not supplied by the upload is initialized to zero, including
  seven reserved metadata words and its flag. Upload/download and textual metadata
  are real; populated legacy download/rating/date meanings remain unresolved.
- Challenge uploads are public to other authenticated accounts for browsing and
  downloading. Names are metadata, never host filesystem paths. The unimplemented
  true filter flag fails explicitly.

The game retains simulation and local progression. Reported records do not prove
an independently validated race result and do not grant cars, money or prizes.
The separate generated prize resource remains a no-pending-awards policy.

## Persistence and bounds

Tenant-local `title-data.json` is private 0600 and versioned. Mutations clone the
snapshot, write/sync a private temporary file and atomically rename before making
the change visible or returning success. A failed commit leaves the prior live
snapshot intact. Existing malformed/non-private/oversized state fails startup;
it is never silently replaced. Back up this file with the tenant's accounts and
keys while stopped.

Current disk format **version 2** stores challenge names and descriptions as
explicit `name_bytes` / `description_bytes` byte arrays encoded with JSON
base64. SDK text is a non-NUL byte string; no UTF-8 assumption or transcoding is
introduced. This preserves legacy character bytes and the full 255-byte limits
across restart. Verified account names remain separate canonical account text;
the network request/reply format and public Go APIs are unchanged.

Valid version-1 state remains readable. Opening it does not rewrite the file;
the next successful mutation atomically commits version 2. A failed mutation
retains the prior file and live snapshot. This migration preserves the text
present in the old file, but cannot recover bytes already replaced by the old
JSON encoder. Malformed old or new state still fails startup without replacement.

Limits are 48 KiB per challenge, 128 challenges, 4096 profiles, 16,384 event
records, twenty ranking rows/event IDs per request and a 24 MiB state file.
Messages/strings are bounded before allocation. Unsupported methods are rejected,
including protocol-103 methods whose response decoders exist but whose linked
PS2 requests/semantics have not been recovered.

## Verification

Tests cover independent field-layout goldens, truncated/trailing/NUL input,
account impersonation rejection, guest rejection, durable report/query results,
challenge upload/list/download and idempotency, missing files, reload/privacy,
failed disk commits and malformed-state retention. Short malformed-record fuzzing
completed 111,877 inputs without failures.

Two sanitized fixtures execute the actual retail record traversal in Unicorn
MIPS64 (`0x2416b0`, `0x24a9d0`), retaining full 64-bit field data. Only 128-bit
stack saves/restores become 64-bit LD/SD, and ordinary scalar/string stream I/O
is intercepted. The fixture provenance explains this limited scope; it is not a
claim that a retail client completed a network report.

A separate real-UDP adapter test creates two authenticated accounts, discovers
native services, joins a FreeRide registry group, resolves the native peer
endpoint, receives a NAT introduction, submits statistics, shares an uploaded
challenge and verifies departure cleanup. Actual stock cruising, races and
reported UI/ranking interpretation still require retail validation.
