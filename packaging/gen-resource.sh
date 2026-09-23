#!/usr/bin/env sh
# Regenerate resource_windows.syso — the VERSIONINFO block, the icon and the
# application manifest that make the .exe look like a Windows program instead of
# an anonymous binary. Run from the repository root.
#
#   packaging/gen-resource.sh 0.1.0
#
# The .syso is committed so that a plain `go build` produces a properly stamped
# exe with no extra tooling. Re-run this when the version or the icon changes.
set -eu
V="${1:-0.1.0}"
MAJOR=$(echo "$V" | cut -d. -f1); MINOR=$(echo "$V" | cut -d. -f2); PATCH=$(echo "$V" | cut -d. -f3)

command -v goversioninfo >/dev/null 2>&1 || {
  echo "need goversioninfo:  go install github.com/josephspurrier/goversioninfo/cmd/goversioninfo@latest" >&2
  exit 1
}
goversioninfo \
  -o resource_windows.syso \
  -platform-specific=false \
  -icon packaging/icon/SmokeTrail.ico \
  -manifest packaging/SmokeTrail.exe.manifest \
  -ver-major "$MAJOR" -ver-minor "$MINOR" -ver-patch "$PATCH" -ver-build 0 \
  -product-ver-major "$MAJOR" -product-ver-minor "$MINOR" -product-ver-patch "$PATCH" -product-ver-build 0 \
  -file-version "$V.0" -product-version "$V.0" \
  packaging/versioninfo.json
echo "wrote resource_windows.syso for $V"
