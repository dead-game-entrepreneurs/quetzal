`native-friends.json` is synthetic, copyright-free protocol20/10 evidence.

- Actual request builder `1f9750` emits method10 and its two argument bytes.
  Real scalar/bool helpers and underlying SDK buffer writes execute. The fixture
  omits the outer RMC header because stream/job setup and scheduling are supplied
  by the harness.
- Actual row reader `1f8f80` consumes a synthetic populated record using real
  scalar/String reader instructions and underlying buffer reads. String capacity
  allocation is supplied; field reads are not mocked.
- Actual list reader `1f9f60` consumes a zero-count response. The empty-list reset
  boundary is supplied; no populated-list allocation is inferred from this case.

Reproduce using the user's hash-verified executable and installed Unicorn:

```sh
python3 internal/games/tdu/social/testdata/verify_friends.py \
  /path/to/tdu-server/tools > /tmp/native-friends.json
cmp /tmp/native-friends.json internal/games/tdu/social/testdata/native-friends.json
```

The verifier reuses the adjacent presence MIPS64 harness. In the scoped routine
ranges it substitutes EE LQ/SQ register saves with LD/SD and low64 PCPYLD with
DADDU. The two EE count MULT instructions at2362f8/236328 use equivalent MIPS MUL
instructions to retain the same low32 count result. Generic memory operations,
stream/job lifecycle, scheduling and String allocation are mocked. No game code,
real account metadata, credentials or network capture are included. This proves
selected codec boundaries, not a full retail startup or social session.
