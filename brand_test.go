package main

import (
	"bytes"
	"strings"
	"testing"
)

// These bytes come from whoever is signed in, and they are served back to every
// operator who opens the console. The sniffing is the only thing standing
// between "someone uploaded a logo" and "someone uploaded a page".

func TestSniffLogoAcceptsRealImages(t *testing.T) {
	cases := map[string][]byte{
		"image/png":  append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, 32)...),
		"image/jpeg": append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, bytes.Repeat([]byte{0}, 32)...),
		"image/gif":  append([]byte("GIF89a"), bytes.Repeat([]byte{0}, 32)...),
		"image/webp": append([]byte("RIFF\x00\x00\x00\x00WEBPVP8 "), bytes.Repeat([]byte{0}, 32)...),
	}
	for want, raw := range cases {
		got, err := sniffLogo(raw)
		if err != nil {
			t.Errorf("%s: %v", want, err)
			continue
		}
		if got != want {
			t.Errorf("sniffed %q, want %q", got, want)
		}
	}
}

// The one that matters. An SVG served from the console's own origin is stored
// XSS against every operator, so it is refused by content rather than by file
// extension — which an uploader controls and this does not.
func TestSniffLogoRefusesSVG(t *testing.T) {
	svgs := [][]byte{
		[]byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`),
		[]byte("\n\t  <?xml version=\"1.0\"?><svg onload=\"alert(1)\"></svg>"),
	}
	for _, raw := range svgs {
		ct, err := sniffLogo(raw)
		if err == nil {
			t.Fatalf("accepted an SVG as %q", ct)
		}
		if !strings.Contains(err.Error(), "SVG") {
			t.Errorf("the refusal should say why, got: %v", err)
		}
	}

	// A .png filename does not make it a PNG, and nothing in this path ever
	// looks at the filename — but assert the content rule directly anyway.
	if _, err := sniffLogo([]byte("<html><body>hi</body></html>")); err == nil {
		t.Fatal("accepted HTML")
	}
}

func TestSniffLogoRefusesOversizeAndJunk(t *testing.T) {
	big := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{7}, maxLogoBytes)...)
	if _, err := sniffLogo(big); err == nil {
		t.Fatal("accepted an image over the size cap")
	} else if !strings.Contains(err.Error(), "KB") {
		t.Errorf("the size refusal should state the limit, got: %v", err)
	}

	if _, err := sniffLogo([]byte("nope")); err == nil {
		t.Fatal("accepted four bytes of nothing")
	}
	if _, err := sniffLogo(bytes.Repeat([]byte{0x42}, 64)); err == nil {
		t.Fatal("accepted arbitrary binary")
	}
}

// Round trip through the settings table, including the media type, because the
// type is what the browser is told and a wrong one is the whole problem.
func TestLogoRoundTrip(t *testing.T) {
	s, err := NewStore(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if _, _, ok := s.Logo(); ok {
		t.Fatal("a fresh store reports a logo")
	}

	png := append([]byte("\x89PNG\r\n\x1a\n"), []byte("pretend pixels")...)
	if err := s.SetLogo(png); err != nil {
		t.Fatal(err)
	}
	raw, ct, ok := s.Logo()
	if !ok {
		t.Fatal("stored logo does not read back")
	}
	if !bytes.Equal(raw, png) {
		t.Fatalf("bytes changed in storage: %q", raw)
	}
	if ct != "image/png" {
		t.Fatalf("media type %q", ct)
	}

	if err := s.ClearLogo(); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := s.Logo(); ok {
		t.Fatal("logo survived being cleared")
	}
}

func TestSetLogoRejectsBeforeStoring(t *testing.T) {
	s, err := NewStore(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.SetLogo([]byte(`<svg onload="alert(1)"></svg>`)); err == nil {
		t.Fatal("SetLogo stored an SVG")
	}
	if _, _, ok := s.Logo(); ok {
		t.Fatal("a rejected upload left something behind")
	}
}
