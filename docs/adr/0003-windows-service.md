# 3. Service runs as LocalService; install is a subcommand

Status: accepted · 2026-09

## Context

A latency monitor that only records while someone is logged in is not a monitor.
The primary Windows deployment has to be a service. Two questions follow: which
account, and how does it get registered.

## Decision

**Account: `NT AUTHORITY\LocalService`.** Not LocalSystem.

This is only possible because of ADR 2. Since probing goes through the ICMP helper
rather than a raw socket, nothing SmokeTrail does needs privilege: it opens a
listening socket, sends ICMP, and writes to one directory. LocalService can do all
three once the installer grants the last one. The service therefore runs with
roughly the rights of a guest account.

This is a security property worth having on its own, and it is also the honest
version of the pitch. "Runs as LocalSystem" would make the least-privilege claim
false.

**Registration: `smoketrail install`, in the binary.**

The subcommand does the work — create the service, grant the data directory, open
the firewall port, register the event log source, ask Defender to exclude the
database — and a graphical installer, when there is one, will call it rather than
reimplement it. One code path, testable from a prompt.

Flags given to `install` are baked into the service command line, so
`smoketrail install --port 9000 --days 90` is replayed on every boot. No credential
is ever passed this way; see ADR 4.

## Consequences

- `install` and `uninstall` need an elevated prompt. Nothing else does.
- Post-install steps (firewall, Defender, event log) are best-effort and log a
  warning on failure. A service that is registered and running should not be rolled
  back because Defender is policy-locked.
- `uninstall` deliberately leaves the data directory. Removing the service is not a
  request to discard the history it collected.
- The ACL grant and the firewall rule shell out to `icacls` and `netsh`. Doing
  either through the Win32 security APIs is a few hundred lines of code to
  reproduce tools that ship with every Windows install and that an administrator
  can run by hand to check our work.
