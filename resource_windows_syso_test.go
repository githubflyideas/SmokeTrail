package main

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// A .syso is not a blob the Go toolchain carries along — it is a COFF object
// file that the linker links into the executable, so it declares a machine type
// and contains machine-specific relocations.
//
// Go selects .syso files by the same filename rules it uses for .go files, so a
// file called resource_windows.syso is offered to EVERY GOOS=windows build. An
// amd64 object handed to the arm64 linker fails with:
//
//	unknown ARM64 relocation type 3
//
// which is what broke the windows/arm64 leg of the cross matrix. Because only
// the release matrix builds arm64, nothing on a developer machine noticed. This
// test makes the machine type a local assertion instead of a CI-only one: it
// reads the COFF header of every committed resource and checks it against the
// GOARCH its filename claims.
//
// See packaging/gen-resource.sh, which must be run with -platform-specific.

// COFF IMAGE_FILE_MACHINE_* constants, from winnt.h.
var sysoMachine = map[string]uint16{
	"386":   0x014c, // I386
	"amd64": 0x8664, // AMD64
	"arm":   0x01c4, // ARMNT — ARMv7 Thumb-2
	"arm64": 0xaa64, // ARM64
}

func TestWindowsResourceObjectsMatchTheirArchitecture(t *testing.T) {
	// The unsuffixed name is the bug itself: it applies to every Windows
	// architecture, so whichever machine it was built for, it is wrong for the
	// others. Its absence is the invariant worth pinning.
	if _, err := os.Stat("resource_windows.syso"); err == nil {
		t.Fatal("resource_windows.syso exists; an unsuffixed resource object is offered to " +
			"every GOOS=windows build and will fail to link on the architectures it was not " +
			"built for. Regenerate with packaging/gen-resource.sh, which passes -platform-specific.")
	}

	found, err := filepath.Glob("resource_windows_*.syso")
	if err != nil {
		t.Fatal(err)
	}
	if len(found) == 0 {
		t.Fatal("no resource_windows_<arch>.syso committed; the released .exe would carry no " +
			"version resource, icon or manifest. Run packaging/gen-resource.sh.")
	}

	seen := map[string]bool{}
	for _, path := range found {
		arch := path[len("resource_windows_") : len(path)-len(".syso")]
		want, known := sysoMachine[arch]
		if !known {
			t.Errorf("%s: %q is not a Windows GOARCH this project knows about", path, arch)
			continue
		}
		seen[arch] = true

		// The COFF file header starts at offset 0 and opens with the machine
		// type as a little-endian uint16.
		head := make([]byte, 2)
		f, err := os.Open(path)
		if err != nil {
			t.Errorf("%s: %v", path, err)
			continue
		}
		_, err = f.Read(head)
		f.Close()
		if err != nil {
			t.Errorf("%s: %v", path, err)
			continue
		}
		if got := binary.LittleEndian.Uint16(head); got != want {
			t.Errorf("%s: COFF machine type is 0x%04x, want 0x%04x for %s. "+
				"This object will not link into a %s binary.", path, got, want, arch, arch)
		}
	}

	// Every architecture the release matrix builds needs its own object, or that
	// leg produces an .exe with no version resource — or fails to link.
	for _, arch := range []string{"386", "amd64", "arm64"} {
		if !seen[arch] {
			t.Errorf("no resource_windows_%s.syso, but the release matrix builds windows/%s", arch, arch)
		}
	}
}
