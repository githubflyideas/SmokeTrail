# SmokeTrail

**Windows has no open-source latency monitor that runs as a service.** It has
excellent throwaway tools — Ping Tracer, vmPing, PingInfoView, `gping` — that you
open when something breaks and close when it stops. It has PRTG and PingPlotter,
which are commercial. What it does not have is the thing SmokePing is on Linux: a
daemon that watches a link for months and lets you go back and look.

SmokeTrail is that, as a single .exe.

```
smoketrail.exe            # run it now, console at http://localhost:8518
smoketrail.exe install    # run it as a service from now on
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

**Service** — from an elevated prompt:

```powershell
smoketrail.exe install                      # or: install --port 9000 --days 90
smoketrail.exe uninstall                    # data is kept
```

Registers under `NT AUTHORITY\LocalService`, not LocalSystem. Data goes to
`%ProgramData%\SmokeTrail`. Logs go to the Application event log:

```powershell
Get-WinEvent -ProviderName SmokeTrail -MaxEvents 30
```

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
smoketrail.exe selftest
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

| | |
|---|---|
| Linux build, full test suite | run, passing |
| `selftest` on linux/amd64 | run, output above |
| Cross-compile windows amd64 / 386 / arm64, `CGO_ENABLED=0` | run, clean |
| `go vet` for linux and windows | run, clean |
| ICMP against a live host on Windows | **not run** — no Windows machine in the loop yet |
| Service install / uninstall / Event Log | **not run** |
| `selftest` on Windows | **not run** — this is the M0 gate |
| Binary size with the real pure-Go SQLite | **not measured**; expect ~20 MB |

The sandbox this was built in could not reach the Go module proxy, so
`modernc.org/sqlite` was stood in for during verification. `go.mod` names the real
module; `go mod tidy && go build` on a normal machine is the first thing to run.

## Credits

The probe engine, storage tiering and chart are [fogping](https://github.com/githubflyideas/fogping),
rewritten here for Windows. Inspired by SmokePing; not affiliated with it, and no
code is shared with it.

MIT.
