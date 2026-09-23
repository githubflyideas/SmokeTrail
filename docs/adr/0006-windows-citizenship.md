# 6. Look and behave like a Windows program, not a Go binary that runs on Windows

Status: accepted · 2026-09

## Context

v0.1.0 compiled for Windows, registered a service, and worked. It was still
obviously not a Windows program:

- Right-click → Properties → **Details was empty**. No product name, no version,
  no company. To an administrator that is an anonymous binary, and to SmartScreen
  and most EDR products it is an unknown one.
- **No icon.** The generic blank-page glyph in Explorer, the taskbar and the
  Services list.
- **No application manifest**, so Windows was free to apply compatibility shims
  and lie to us about its own version.
- **`pingping.exe`**, lowercase, next to `pingping` in Services.msc.
- `install` **failed with an error** telling the operator to go and open an
  elevated prompt, instead of asking for elevation the way every other Windows
  installer does.
- The service had **no failure actions**, so one crash left it stopped until a
  human noticed — and a stopped latency monitor draws a flat line that looks
  exactly like a healthy link.
- Every event went to the Application log as **event ID 1**, which is unfilterable
  and therefore un-alertable.

None of that is cosmetic. Each item is something a Windows administrator uses to
decide whether software is trustworthy and operable.

## Decision

Meet the platform's conventions.

**Version resource, icon and manifest**, compiled into `resource_windows.syso` and
committed, so a plain `go build` produces a properly stamped exe with no extra
tooling. `packaging/gen-resource.sh` regenerates it; the icon is generated from
`packaging/icon/make_icon.py` rather than being an opaque binary nobody can edit.

The manifest requests **`asInvoker`**, not `requireAdministrator`. Running the
console needs no privilege — that is the whole point of ADR 2 — and an application
that prompts on every ordinary run teaches people to click through prompts.

**`install` and `uninstall` elevate themselves** with the `runas` verb. Typing the
command in a normal prompt produces a consent dialog, which is what a Windows
administrator expects. The elevated child gets its own console that closes
instantly, so it is handed a log file and the waiting parent streams it: from the
operator's side the command simply works in the window they typed it in.

**A per-service SID.** Declaring `SERVICE_SID_TYPE_UNRESTRICTED` gets the service
`NT SERVICE\pingping`, and the data directory is ACL'd to that rather than to
`LocalService`. There are many LocalService processes on a Windows box; now none
of the others can read the database. One flag, one ACL, and "low privilege"
becomes "low privilege and isolated".

**Failure actions**: restart after 5 s, 20 s, 60 s, counter reset daily.

**Delayed automatic start**, with `Tcpip` and `Dnscache` dependencies. Probing
before the stack is up is pointless, and nothing else on the machine waits on us,
so we yield the boot path to services that something does wait on.

**Event IDs as a public interface** — 1xxx lifecycle, 2xxx degraded, 3xxx failed
to start — plus service-specific exit codes so `sc query` distinguishes "the port
was taken" from "the database would not open". Numbers are never reused or
renumbered, because someone's alert rule depends on them.

**`pingping.exe`**, matching the product name shown in Services.msc, Task
Manager and the file's own version resource. Unix artifacts stay lowercase.

**NSIS, not Inno Setup**, for the installer. Inno has the nicer default wizard,
but `makensis` runs on Linux, so binaries and installer come out of one ubuntu job
with no Windows runner anywhere in the release path. That is the same principle as
ADR 1 — one toolchain, no per-platform build machines — applied one layer up.

## Consequences

- A plain `go build` now depends on a committed binary (`resource_windows.syso`,
  ~70 KB). That is the accepted cost of not requiring a resource compiler to
  produce a correct build. Both the generator script and the icon's source are in
  the repository, so it is reproducible rather than magic.
- The elevation path has three shapes of invocation — an operator in a normal
  prompt, the elevated child, and the installer, which is already elevated — and
  each must take exactly one branch. That logic is in `runServiceVerb`, kept in
  one function for that reason.
- Code signing is now the one remaining gap between this and software an
  enterprise will deploy without argument. The version resource makes signing
  worth more, since a signed binary with a real identity block is what builds
  SmartScreen reputation. Still deferred; see the README.
