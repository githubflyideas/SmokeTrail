package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
)

// A custom logo for the console header.
//
// This exists because someone deploying pingping on a customer's network will
// want the screen that customer looks at to carry the customer's mark, and that
// is a reasonable thing to want. The licence does not require anything in
// return, so neither does the software: there is no key to enter and nothing is
// withheld. Apache 2.0 already permits rebranding, and a feature that pretends
// otherwise would only be theatre.
//
// It lives in the settings table rather than as a file beside the database so
// that the brand travels with the data: copy the .db to another machine and the
// console there looks the same. One directory remains one directory, which is
// the promise portable mode makes.
//
// SVG is deliberately not accepted. An SVG is a document that can carry script,
// and serving one from the console's own origin turns a logo upload into stored
// XSS against every operator who opens the page. Every real logo exists as a
// PNG, and refusing the format is cheaper than defending it.

const (
	settingLogo     = "brand_logo"      // base64 of the image bytes
	settingLogoType = "brand_logo_type" // the sniffed media type

	// Large enough for a detailed PNG at retina size, small enough that it can
	// never turn the database into a file store. The header renders it at 30px.
	maxLogoBytes = 256 * 1024
)

// sniffLogo identifies the image from its own bytes. The Content-Type header is
// whatever the uploader felt like sending, so it decides nothing here.
func sniffLogo(b []byte) (string, error) {
	switch {
	case len(b) > maxLogoBytes:
		return "", fmt.Errorf("image is %d KB; the limit is %d KB", len(b)/1024, maxLogoBytes/1024)
	case len(b) < 16:
		return "", fmt.Errorf("that is not an image")

	case bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")):
		return "image/png", nil
	case bytes.HasPrefix(b, []byte{0xFF, 0xD8, 0xFF}):
		return "image/jpeg", nil
	case bytes.HasPrefix(b, []byte("GIF87a")), bytes.HasPrefix(b, []byte("GIF89a")):
		return "image/gif", nil
	case bytes.HasPrefix(b, []byte("RIFF")) && bytes.Equal(b[8:12], []byte("WEBP")):
		return "image/webp", nil

	case bytes.HasPrefix(bytes.TrimLeft(b, " \t\r\n"), []byte("<")):
		return "", fmt.Errorf("SVG is not accepted because it can carry script; export the logo as a PNG")
	}
	return "", fmt.Errorf("unrecognised image format; use PNG, JPEG, GIF or WebP")
}

// Logo returns the stored image and its media type. The bool reports whether one
// is set at all, which is what the console asks before deciding to draw its own
// mark instead.
func (s *Store) Logo() ([]byte, string, bool) {
	enc, ok := s.Setting(settingLogo)
	if !ok || enc == "" {
		return nil, "", false
	}
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return nil, "", false
	}
	ct, ok := s.Setting(settingLogoType)
	if !ok {
		// Stored by a version that did not record the type, or edited by hand.
		// Sniff it again rather than serving it as something the browser guesses.
		if ct, err = sniffLogo(raw); err != nil {
			return nil, "", false
		}
	}
	return raw, ct, true
}

func (s *Store) SetLogo(raw []byte) error {
	ct, err := sniffLogo(raw)
	if err != nil {
		return err
	}
	if err := s.SetSetting(settingLogo, base64.StdEncoding.EncodeToString(raw)); err != nil {
		return err
	}
	return s.SetSetting(settingLogoType, ct)
}

func (s *Store) ClearLogo() error {
	if err := s.SetSetting(settingLogo, ""); err != nil {
		return err
	}
	return s.SetSetting(settingLogoType, "")
}
