# Force Match Start

The original PS2 manual describes a supported Force Match Start feature: start
with ready players and return unready players to the lobby. It does not promise
to make every connected player participate. See the staging-screen section on
printed page 17 of the
[publisher-hosted PS2 manual](https://support.na.square-enix.com/document/manual/982/25_to_Life_PS2.pdf#page=10).

The earlier two-player test left the only player on one team unready, expecting
Force Match Start to bring both players into the match. That was the wrong
expected result. For a mode requiring two populated teams, excluding that
unready player leaves one team empty even though the staging screen shows a
player on each side. The reported empty-Criminals error is consistent with that
test configuration; it does not establish an unsupported stock feature.

## Retail code trace

Evidence below is for the unmodified US PS2 `SLUS_210.16` executable, SHA-256
`52963efa0a12a44db01dbfad65d9bbb865c802c7ddf4513855913314b69cdba3`.

- `0x00121d18` registers the Ready toggle, unready and Force Ready UI events.
  The Force Ready listener is the member at offset `0x50`; its callback table
  contains `0x00121f30`.
- `0x00121f30` handles the local participant and calls `0x00244b50`. The latter
  requests host countdown mode 2; the ordinary path at `0x00244b90` uses mode 1.
  This callback does not loop over remote players to mark all of them ready.
- `0x00244bf0` checks both teams' eligible counts and produces the too-many-
  players or empty-team UI messages. It calls counters `0x00139128` and
  `0x00139198`.
- Those counters resolve the current team mapping and call `0x00139088`.
  That function counts team members only when the per-player predicate
  `0x002452e0` permits them. The predicate consults native player flags through
  `0x001219e0` and `0x00121aa8`; it is not the backend room-member count.
- The native round setup path at `0x001df0f8` identifies players for the round.
  The manual supplies the user-facing meaning of the ready/unready behavior;
  the native start/validation path is consistent with it.

This was checked with read-only Ghidra inspection. Some older private exports
started inside functions (for example `0x00121e10` instead of the real entry
`0x00121e18`); those incomplete exports are not evidence for unsupported client
behavior. No client patch or emulator modification is part of this work.

## Test that distinguishes Force Match Start from normal readiness

Use a three-player staging room in a mode requiring both teams:

1. Put one ready player on Cops and one ready player on Criminals.
2. Keep the third player unready so ordinary all-ready startup cannot mask the
   test.
3. Have the host select Force Match Start.

The expected result is that the ready players start and the unready player
returns to the lobby. A team-empty error in this configuration, failure to start,
or failure to return the excluded player would warrant a fresh native ready-state
and start/exit trace. No such successful three-player stock-client test has been
confirmed yet. The prior successful both-ready two-player match verified normal
startup, not this distinct case.

There is no demonstrated backend correction to deploy from the earlier
two-player empty-team result alone. Preserve the stock team checks, existing
room/statistics behavior and real-console compatibility while testing the
documented flow.
