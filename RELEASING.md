# Releasing

Two artefacts live in two different places, and mixing them up is the usual
mistake:

| | Where | What |
|---|---|---|
| **Source** | the git repository | source only — no binaries, no .zip, no setup.exe |
| **Installer and portable .zip** | the **Releases** page | attached to a tag, with checksums |

Windows users download from Releases. Nothing they need is committed to the
repository, and nothing built is either.

## Cutting a release

```bash
git tag -a v0.2.1 -m "SmokeTrail v0.2.1"
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
4. builds `SmokeTrail-<version>-setup.exe` with `makensis`, on Ubuntu;
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
