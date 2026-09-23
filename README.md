# SmokeTrail

**Windows has no open-source latency monitor that runs as a service.** It has
excellent throwaway tools — Ping Tracer, vmPing, PingInfoView, `gping` — that you
open when something breaks and close when it stops. It has PRTG and PingPlotter,
which are commercial. What it does not have is the thing SmokePing is on Linux: a
daemon that watches a link for months and lets you go back and look.

SmokeTrail is that, as a single .exe.

```
SmokeTrail.exe            # run it now, console at http://localhost:8518
SmokeTrail.exe install    # run it as a service from now on  (asks for elevation)
```

No .NET, no runtime, no DLL beside it, no Perl, no RRDtool, no WSL. One file.

> Status: **v0.1.0, first release.** The Linux engine is fogping's, in production.
> The Windows layer — ICMP, service, paths, install — is new and wants testing on
> real hardware. See *Verification status* below for exactly what has and has not
> been run.

---

## It records the shape, not the average

A link that averages 20 ms but spikes to 400 ms at p99 and a link that sits at a
flat 20 ms have the same average and nothing else in common. Averages are where
that difference goes to die, and most monitoring stores averages.

SmokeTrail keeps **every RTT sample** for the recent window, and when it ages data
out it keeps min / p50 / p90 / p99 / max, loss, and burst counts per hour — for 300
days. Zoom out to a quarter and the smoke is still smoke, not a line.

Loss bursts are flagged with a robust z-score (median + MAD, Iglewicz–Hoaglin
cutoff) rather than a threshold you have to tune. The chart marks them; you decide
what they meant.

## Install

**Portable** — unzip, double-click `smoketrail.exe`. Everything lives in the
`data` folder beside it. Nothing is written elsewhere, no registry key is touched,
and deleting the folder is a complete uninstall. This is the form for a customer
site where you cannot install software.

**Service** — from any prompt; it asks for elevation itself.

```powershell
SmokeTrail.exe install                      # or: install --port 9000 --days 90
SmokeTrail.exe uninstall                    # data is kept
```

| | |
|---|---|
| account | `NT AUTHORITY\LocalService` with a per-service SID, so the database is locked to this service — not to every other LocalService process on the box |
| start | automatic (delayed) |
| recovery | restart after 5s, 20s, 60s |
| data | `%ProgramData%\SmokeTrail` |
| firewall | console port, private + domain profiles |
| Defender | exclusion for the database |

Logs go to the Application event log with filterable event IDs — 1xxx lifecycle,
2xxx degraded, 3xxx failed to start:

```powershell
Get-WinEvent -ProviderName SmokeTrail -MaxEvents 30
```

The .exe carries a version resource, an icon and an application manifest, so
Properties → Details reads properly and Windows applies no compatibility shims.
The manifest asks for `asInvoker`: running the console never prompts, because it
never needs privilege.

**Linux** — same binary, same console. `deploy/smoketrail.service` is the unit
file. ICMP without root needs one of:

```bash
echo 'net.ipv4.ping_group_range = 0 2147483647' | sudo tee /etc/sysctl.d/99-smoketrail.conf && sudo sysctl --system
# or
sudo setcap cap_net_raw+ep ./smoketrail
```

**First run:** open the console and it asks you to create the admin account. There
is no default password, no password on the command line, and no config file. A
service command line lives in the registry where every account can read it, so a
credential does not belong there ([ADR 4](docs/adr/0004-auth.md)).

## Check your machine before you trust the graphs

```
SmokeTrail.exe selftest
```

SmokeTrail claims sub-millisecond RTT differences are real and worth drawing. That
is only true if the host's clock resolves finely enough and its scheduler wakes
goroutines on time. Windows historically ran a ~15.6 ms timer tick, which would
fail badly. `selftest` measures both and says PASS or FAIL.

```
clock resolution      21ns
  PASS  fine enough for sub-millisecond RTT
sleep 50ms   overshoot  p50 270.112µs  p90 319.06µs   p99 380.528µs  max 426.705µs
  PASS  pacing is tight; packets in a round are evenly spaced
ticker drift over 100  ticks of 20ms   308.498µs total (3.084µs per tick)
  PASS  probe intervals will hold over days
```

*(linux/amd64, Go 1.24.7. Post your Windows numbers in an issue — that is the
table this README wants.)*

## How the Windows port works

Five decisions, each written up because the reasoning is the interesting part:

| | |
|---|---|
| [ADR 1](docs/adr/0001-sqlite-driver.md) | Pure-Go SQLite, so `CGO_ENABLED=0` and no mingw in CI |
| [ADR 2](docs/adr/0002-windows-icmp.md) | `IcmpSendEcho2`, not a raw socket — and why its `RoundTripTime` field is unusable |
| [ADR 3](docs/adr/0003-windows-service.md) | LocalService, not LocalSystem |
| [ADR 4](docs/adr/0004-auth.md) | First-run setup, not credentials on argv |
| [ADR 5](docs/adr/0005-portable-and-installed.md) | One binary, two personalities, one directory decides |
| [ADR 6](docs/adr/0006-windows-citizenship.md) | Version resource, icon, manifest, self-elevation, per-service SID, failure actions, event IDs |

The short version of the interesting one: `ICMP_ECHO_REPLY.RoundTripTime` is a
`ULONG` **of milliseconds**. For a tool that draws distributions, that quantises a
healthy LAN into a staircase of 0s and 1s. SmokeTrail ignores the field and times
the call itself against `QueryPerformanceCounter`.

## Build

```bash
go mod tidy                                            # once — there is no go.sum yet, see below
go build .                                             # host
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build .     # windows
```

That is the whole cross-compile story, and keeping it that way is what ADR 1 buys.

## Verification status

Being precise about this, because "it compiles" and "it works" are different claims.

**Run, passing:**

| | |
|---|---|
| Linux build + full test suite | clean |
| `go vet`, linux and windows (amd64 + 386) | clean |
| Cross-compile windows amd64 / 386 / arm64, `CGO_ENABLED=0` | clean |
| **End-to-end console**: first-run setup → login → add target → probe → `/api/series` → logout → graceful shutdown | clean, see below |
| `selftest` on linux/amd64 | clean, output above |
| `IcmpSendEcho2` signature and `ICMP_ECHO_REPLY` layout vs. Microsoft docs | matches |
| Struct offsets for both pointer widths (40 B on amd64, 28 B on 386) | matches the C ABI |

The end-to-end run also produced the number that justifies ADR 2. A loopback TCP
target measured **p50 = 0.135 ms, p90 = 0.238 ms, p99 = 0.274 ms** over 20 samples
per round. Every one of those values rounds to 0 or 1 in a millisecond field, which
is exactly what `ICMP_ECHO_REPLY.RoundTripTime` would have given us.

**Not run — needs real Windows hardware:**

| | |
|---|---|
| ICMP against a live host | the `IcmpSendEcho2` call path is checked against the docs and asserted by tests, but has never executed |
| Service install / uninstall / Event Log | — |
| `selftest` on Windows | **this is the open M0 gate** |
| Inno Setup script compiles | no `iscc` available here |
| Binary size with the real pure-Go SQLite | not measured; expect ~20 MB |

`icmp_windows_test.go` asserts the struct layout on every Windows CI job and on
each architecture shipped. A wrong field offset would not crash — it would silently
read the wrong bytes and report plausible nonsense, which is the worst failure mode
a measurement tool has, so it is pinned by a test rather than by a comment.

The sandbox this was built in could not reach the Go module proxy, so
`modernc.org/sqlite` was stood in for during verification. `go.mod` names the real
module; `go mod tidy && go build` on a normal machine is the first thing to run.

## Credits

The probe engine, storage tiering and chart are [fogping](https://github.com/githubflyideas/fogping),
rewritten here for Windows. Inspired by SmokePing; not affiliated with it, and no
code is shared with it.

Apache License 2.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
