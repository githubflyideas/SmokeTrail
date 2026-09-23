//go:build !windows

package main

import (
	"os"
	"strings"
)

// The catalogue is only consumed by the notification-area icon today, which is
// Windows-only. This exists so the console can use the same strings later without
// the package splitting in two.
func preferredLang() string {
	for _, v := range []string{os.Getenv("LC_ALL"), os.Getenv("LC_MESSAGES"), os.Getenv("LANG")} {
		if i := strings.IndexAny(v, ".@"); i > 0 {
			v = v[:i]
		}
		if l := normalizeLang(v); l != "" {
			return l
		}
	}
	return langEN
}
