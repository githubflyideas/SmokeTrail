# Releasing

Two artefacts live in two different places, and mixing them up is the usual
mistake:

| | Where | What |
|---|---|---|
| **Source** | the git repository | source only — no binaries, no .zip, no setup.exe |
| **Installer and portable .zip** | the **Releases** page | attached to a tag, with checksums |

Windows users download from Releases. Nothing they need is committed to the
repository, and nothing built is either.

## Binaries come from CI. Always.

Never hand someone a binary built anywhere else — not from a developer machine,
not from a sandbox, not "just to test". The SQLite driver is selected at build
time, so a binary built in an environment that cannot fetch `modernc.org/sqlite`
compiles, links, starts, serves its first page, and then cannot store a single
measurement. On Windows the first thing to touch the database is service
registration, which reports:

    The files were installed, but the pingping service could not be registered (exit 1)

and says nothing about a database. That message cost a night. The build that
produced it was green; `go build` succeeding proves the program compiles and
nothing more.

Two things now stand in the way, and neither should be removed:

- `pingping selftest` opens a database, writes and reads back, and prints a
  storage verdict. Anyone holding a copy they are unsure about can settle it in
  one command.
- The `build` and `release` workflows run the binary they just built — selftest,
  then first-run setup, sign-in, create a target and read it back over the HTTP
  API, then assert the database exists on disk. Nothing is published that has
  not been run.

If you need to check a change on Windows before a release, push a branch and
take the artifact from the build workflow. Do not build it locally and send it.

## Cutting a release

```bash
git tag -a v0.2.1 -m "pingping v0.2.1"
git push origin v0.2.1
```

That is the whole procedure. `.github/workflows/release.yml` then:

1. stamps `resource_windows.syso` with the tag's version, so the .exe's
   Properties → Details matches the release it came from;
2. cross-compiles six targets with `CGO_ENABLED=0` — windows amd64/386/arm64,
   linux amd64/arm64, darwin arm64;
3. builds the portable .zip, which ships with an empty `data` folder. That
   folder is the entire portable-versus-installed switch (ADR 5), so it has to
   be there and must not be in the installer;
4. builds `pingping-<version>-setup.exe` with `makensis`, on Ubuntu;
5. publishes everything with a `SHA256SUMS.txt`.

No Windows runner is involved in producing a release. If that ever changes, look
at what was added: it usually means something reintroduced cgo.

## Before tagging

- `go test ./...` passes
- `go vet` is clean for linux, windows/amd64 and windows/386
- the version in `packaging/versioninfo.json` is not load-bearing — CI overrides
  it from the tag — but keep it roughly current so local builds are not confusing
- install the previous release, then install the new one over it, and check that
  the service comes back and the history survives

## The one thing still missing

The binaries are **unsigned**, so SmartScreen warns on first run and some
managed environments will refuse them outright. A code-signing certificate is
the single remaining gap between "it works" and "an enterprise will deploy it
without argument", and the version resource added in ADR 6 is what makes signing
worth paying for: a signed binary with a real identity block is what accumulates
SmartScreen reputation.
