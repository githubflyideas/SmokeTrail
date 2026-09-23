package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Legacy data migration.
//
// This program has been renamed twice: fogping -> SmokeTrail -> pingping. Each
// rename moved the database filename, and on Windows the second one also moved
// the data directory, from %ProgramData%\SmokeTrail to %ProgramData%\pingping.
//
// An upgraded install that cannot find its own history is indistinguishable, from
// the operator's side, from one that lost it — and this database is the only copy
// of up to 300 days of measurements. So before concluding it is a fresh install,
// the binary looks where its predecessors kept theirs.
//
// Two rules keep this safe:
//
//   - Migrate only into an ABSENT destination. If pingping.db already exists this
//     does nothing at all, so it can never overwrite live data and is a no-op on
//     every run after the first.
//   - Move, never merge. Two databases are never combined; the first legacy file
//     found wins and the rest are left untouched for the operator to look at.
//
// A portable copy never reaches into the system directory. Its data lives beside
// the executable by definition, and a portable unzip must not adopt — or worse,
// move away — the database belonging to an installed service on the same machine.

const dbFileName = "pingping.db"

// legacyDBNames are the database filenames this program used before, newest
// first, so an install that skipped a version still finds the most recent one.
var legacyDBNames = []string{"smoketrail.db", "fogping.db"}

// migrateLegacyData moves a predecessor's database into place and reports which
// file it came from. An empty return with a nil error means there was nothing to
// do, which is the normal case.
func migrateLegacyData(dir string, portable bool) (string, error) {
	dst := filepath.Join(dir, dbFileName)
	if _, err := os.Stat(dst); err == nil {
		return "", nil // already ours; never touch it
	}

	dirs := []string{dir}
	if !portable {
		dirs = append(dirs, legacySystemDirs()...)
	}

	for _, d := range dirs {
		for _, name := range legacyDBNames {
			src := filepath.Join(d, name)
			if src == dst {
				continue
			}
			if _, err := os.Stat(src); err != nil {
				continue
			}
			if err := moveDatabase(src, dst); err != nil {
				return "", fmt.Errorf("migrating %s to %s: %w", src, dst, err)
			}
			return src, nil
		}
	}
	return "", nil
}

// moveDatabase moves a SQLite database and its sidecars together. The -wal file
// holds committed transactions that have not been checkpointed into the main
// file yet, so moving the .db alone can silently drop the most recent rounds.
func moveDatabase(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		s, d := src+suffix, dst+suffix
		if _, err := os.Stat(s); err != nil {
			continue
		}
		if err := os.Rename(s, d); err == nil {
			continue
		}
		// Rename fails across filesystems — %ProgramData% and a data directory on
		// another drive, or a container bind mount. Copy, then remove the source
		// only once the copy is safely on disk.
		if err := copyFile(s, d); err != nil {
			return err
		}
		if err := os.Remove(s); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	// Sync before reporting success: the source is deleted on the strength of
	// this copy, so "written" has to mean on disk, not in the page cache.
	if err := out.Sync(); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	return out.Close()
}
