#!/usr/bin/env sh
# Regenerate the Windows resource objects — the VERSIONINFO block, the icon and
# the application manifest that make the .exe look like a Windows program
# instead of an anonymous binary. Run from the repository root.
#
#   packaging/gen-resource.sh 0.3.0
#
# ONE FILE PER ARCHITECTURE, and that is not cosmetic. A .syso is a COFF object
# that the Go linker links into the executable, so it carries a machine type and
# machine-specific relocations. A single `resource_windows.syso` matches every
# GOOS=windows build by Go's filename rules, so an amd64 object gets handed to
# the arm64 linker, which fails with:
#
#   unknown ARM64 relocation type 3
#
# `-platform-specific` makes goversioninfo write resource_windows_386.syso,
# _amd64, _arm and _arm64 instead, and Go's GOARCH suffix rule then gives each
# linker the object built for it.
#
# The .syso files are committed so that a plain `go build` produces a properly
# stamped exe with no extra tooling. Re-run this when the version or icon changes.
set -eu
V="${1:-0.1.0}"
MAJOR=$(echo "$V" | cut -d. -f1); MINOR=$(echo "$V" | cut -d. -f2); PATCH=$(echo "$V" | cut -d. -f3)

command -v goversioninfo >/dev/null 2>&1 || {
  echo "need goversioninfo:  go install github.com/josephspurrier/goversioninfo/cmd/goversioninfo@latest" >&2
  exit 1
}

rm -f resource_windows.syso resource_windows_*.syso

goversioninfo \
  -platform-specific \
  -icon packaging/icon/pingping.ico \
  -manifest packaging/pingping.exe.manifest \
  -ver-major "$MAJOR" -ver-minor "$MINOR" -ver-patch "$PATCH" -ver-build 0 \
  -product-ver-major "$MAJOR" -product-ver-minor "$MINOR" -product-ver-patch "$PATCH" -product-ver-build 0 \
  -file-version "$V.0" -product-version "$V.0" \
  packaging/versioninfo.json

ls -1 resource_windows_*.syso | sed "s/^/wrote /"
