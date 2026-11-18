# Native bootstrap address fixture

`native-bootstrap-address.json` contains synthetic IPv4 addresses only. The
verified US SLUS-214.90 ELF SHA-256 is
`620ea714b1dfd2d003a9791e5fabc2e8c9f1f6ac5c908f5873cd7e54972c7bf5`.

The private research script `work/gameplay/verify_bootstrap_address.py` in the
sibling `tdu-server` workspace executes retail response dispatcher `0x247b10`,
raw reader `0x236320` / `0x236230`, getter `0x1e2c20`, online-entry endian
conversion `0x13aca0`, connection constructor `0x1553b0` and UDP sender
`0x142c60`. It records the sockaddr bytes at sendto boundary `0x26fd08`.
Both network-order and reversed scalar encodings execute the same path.

The harness supplies output-slot selection, byte-buffer storage, clock,
semaphores, allocation and text-copy framework operations. Crypto allocation
returns null because the initial master datagram does not use that object.
Native request construction and every address conversion execute retail code.
Scoped EE normalization retains the low word of register saves, 64-bit adds,
zero/sign extensions and the raw reader's byte-count multiply. This is a
limited offline routine check; it does not execute the entire online-entry
manager or establish retail gameplay compatibility.

Regenerate from the private research checkout:

```sh
python3 work/gameplay/verify_bootstrap_address.py > work/gameplay/native-bootstrap-address.json
```

No executable, decompiled code, login proof or capture is included here.
