# 5. One binary, two personalities, decided by one directory

Status: accepted · 2026-09

## Context

Two Windows deployments matter and they want opposite things:

- **Installed.** Runs as a service, data belongs in `%ProgramData%`, survives the
  account that installed it.
- **Portable.** An engineer copies one .exe onto a customer's jump box, watches a
  link for two days, and leaves without a trace. Many customer environments do not
  permit installing anything at all, so this is not a convenience — for that
  audience it is the only usable form.

A flag could select between them, but a flag is a thing to get wrong, document, and
support.

## Decision

One rule, checked at startup:

    --data DIR given            →  use it
    <exe dir>/data exists       →  portable: everything stays beside the binary
    otherwise                   →  %ProgramData%\SmokeTrail  (Windows)
                                   ./data                    (Unix)

The portable .zip ships with an empty `data` directory. The installer does not.
That single difference makes each build behave correctly with no flag, and nothing
for the user to understand.

## Consequences

- A portable copy writes nothing outside its own folder and touches no registry key.
  Deleting the folder is a complete uninstall.
- `install` resolves the directory itself and pins it with an explicit `--data` on
  the service command line, because a service starts with an arbitrary working
  directory and a relative default would land somewhere surprising.
- The Unix fallback stays `./data`, which is what every fogping release used. An
  in-place upgrade must not silently start a new database somewhere else.
- Dropping a `data` directory next to an installed .exe would flip it to portable
  on next start. Acceptable: it takes deliberate action, and the service pins
  `--data` anyway, so it cannot happen by accident to an installed instance.
