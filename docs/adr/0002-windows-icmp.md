# 2. Windows ICMP via iphlpapi!IcmpSendEcho2

Status: accepted · 2026-09

## Context

On Linux, pingping opens `SOCK_DGRAM`/`IPPROTO_ICMP` — an unprivileged ICMP
socket — and falls back to a raw socket. Windows has no equivalent. The choices
are:

1. **Raw socket.** Closest to the existing code, but raw sockets require
   Administrator. The service would have to run as LocalSystem, and running the
   .exe by hand would need an elevated prompt — which kills both the portable
   double-click story and the least-privilege service story.
2. **`iphlpapi!IcmpSendEcho2`.** The documented Windows ICMP helper. Any user may
   call it. Costs a syscall wrapper and a different concurrency model.

## Decision

Use `IcmpSendEcho2`, called through `syscall`/`x/sys/windows` with no third-party
ping library.

## Consequences

Two things fall out of this, and both are visible in `icmp_windows.go`.

### RoundTripTime is useless to us

`ICMP_ECHO_REPLY.RoundTripTime` is a `ULONG` **of milliseconds**. For a tool whose
entire premise is the shape of an RTT distribution, 1 ms buckets destroy the
signal: a healthy LAN at 0.3 ms would render as a flat line alternating between 0
and 1, and that staircase would be an artifact of the API, not a property of the
link.

So the field is ignored. We time the call ourselves with `time.Now()`, which Go
backs with `QueryPerformanceCounter` on Windows — sub-microsecond. The cost is that
our number includes the syscall round trip through the helper. That is a roughly
fixed overhead rather than a source of jitter, but "roughly" is not a measurement,
which is why `pingping selftest` exists and why it ships in the binary.

**This was the finding that justified doing M0 before anything else.** If the clock
had turned out coarse, the product premise would not have held on Windows and the
port would have been the wrong thing to build.

### Packets go out concurrently

The synchronous form blocks its thread for the full timeout when a packet is lost.
A 20-packet round sent serially against a black hole would take 20 s of wall clock
and overrun even the slow pace.

Packets therefore go out from separate goroutines, staggered by `gap` so the send
pattern matches Unix, with concurrency capped at `inFlight` (8). With the default
50 ms gap this never throttles a link whose RTT is under 400 ms, so healthy targets
are paced identically on both platforms; a fully black-holed round degrades to
`ceil(packets/inFlight) * timeout` instead of `packets * timeout`.

## Also decided here: TCP probing is a full connect

Windows has blocked raw TCP sends since XP SP2, so a half-open SYN probe would
need Npcap — a driver install, which would end the single-binary story. Both
platforms therefore time a complete `connect()`. This is stated in the UI rather
than hidden: the number includes the remote stack's accept path, not just the link.
