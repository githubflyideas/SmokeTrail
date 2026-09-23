package main

import (
	"io"
	"runtime"
	"strings"
)

// Help is examples first, prose second. Someone reading this has already
// downloaded the thing and wants a line they can paste.

const helpCommon = `SmokeTrail {{VERSION}} — latency distribution and packet loss, as a service.

  Not an average. SmokeTrail keeps every RTT sample so the chart shows the SHAPE
  of a link's behaviour — the spread, the outliers, the loss bursts — for {{DAYS}} days.

USAGE
  {{EXE}} [command] [flags]

COMMANDS
  (none) / run     run in the foreground; Ctrl-C stops it
  install          {{INSTALLDESC}}
  uninstall        {{UNINSTALLDESC}}
  selftest         measure this machine's clock and scheduler (see below)
  version          print the version
  help             this text

FLAGS
  --port N         console port (default 8518)
  --localhost      bind 127.0.0.1 only — nobody else on the network can reach it
  --days N         days of history to keep (default 300)
  --data DIR       where the database lives (see DATA below)

FIRST RUN
  There is no password on the command line and no config file to edit. Start it,
  open the console, and the first screen asks you to create the admin account.
  Everything after that — targets, pacing, the password itself — is changed in the
  console. A service command line is readable by every account on the machine, so
  a credential does not belong there.

SELFTEST
    {{EXE}} selftest

  SmokeTrail's premise is that sub-millisecond RTT differences are real and worth
  drawing. That is only true if the host's clock resolves finely enough and its
  scheduler wakes goroutines on time. selftest measures both and tells you PASS or
  FAIL. Run it once on any machine before trusting the graphs it draws.
`

const helpWindows = `
DATA
  Portable   a "data" folder next to SmokeTrail.exe → everything stays there,
             nothing is written outside the folder, nothing touches the registry.
  Installed  no such folder → %ProgramData%\SmokeTrail

  That is the whole rule. The portable .zip ships with an empty data folder and
  the installer does not, so each build does the right thing with no flag.

EXAMPLES
    SmokeTrail.exe                          run it now, in this window
    SmokeTrail.exe --port 9000              ...on another port
    SmokeTrail.exe install                  run as a service from now on
    SmokeTrail.exe install --port 9000 --days 90
    SmokeTrail.exe uninstall                remove the service (data is kept)

  install and uninstall need Administrator and will ask for it — accept the
  prompt and the command finishes in the window you typed it in. Nothing else
  prompts: running the console needs no privilege at all.

  What install sets up:
    account    NT AUTHORITY\LocalService, with a per-service SID, so the
               database is locked to this service and not to every other
               LocalService process on the machine
    start      automatic (delayed) — the network is up before we probe
    recovery   restart after 5s, 20s, 60s; a monitor that stays down after a
               crash draws a flat line that looks like a healthy link
    firewall   the console port, on the private and domain profiles
    Defender   an exclusion for the database, because real-time scanning shows
               up as I/O jitter in the measurements

  Service logs go to the Application event log, with event IDs you can filter on
  (1xxx lifecycle, 2xxx degraded, 3xxx failed to start):
    Get-WinEvent -ProviderName SmokeTrail -MaxEvents 30

  Upgrade: stop the service, replace SmokeTrail.exe, start it again.
`

const helpUnix = `
DATA
  Portable   a "data" folder next to the binary → everything stays there
  Otherwise  ./data, relative to the working directory

EXAMPLES
    ./smoketrail                            run it now
    ./smoketrail --port 9000 --days 90
    ./smoketrail --localhost                bind loopback only

  ICMP without root — pick one:
    echo 'net.ipv4.ping_group_range = 0 2147483647' | sudo tee /etc/sysctl.d/99-smoketrail.conf
    sudo sysctl --system
  or:
    sudo setcap cap_net_raw+ep ./smoketrail

  Run it as a service with the unit file in deploy/smoketrail.service.
`

const helpFooter = `
Source, ADRs and the Windows notes: https://github.com/githubflyideas/SmokeTrail
`

func printHelp(w io.Writer) {
	body := helpUnix
	if runtime.GOOS == "windows" {
		body = helpWindows
	}
	install, uninstall, exe := "(Windows only)", "(Windows only)", "./smoketrail"
	if runtime.GOOS == "windows" {
		install = "register as a Windows service (asks for elevation)"
		uninstall = "stop and remove the service (data is kept)"
		exe = "SmokeTrail.exe"
	}
	t := helpCommon + body + helpFooter
	r := strings.NewReplacer(
		"{{VERSION}}", version,
		"{{DAYS}}", "300",
		"{{INSTALLDESC}}", install,
		"{{UNINSTALLDESC}}", uninstall,
		"{{EXE}}", exe,
	)
	io.WriteString(w, r.Replace(t))
}
