package main

import (
	"regexp"
	"strings"
	"testing"
)

// Every write from the console must carry a JSON content type. The server
// requires it — that requirement is the CSRF defence, since a JSON content type
// forces a CORS preflight we never answer — and a page that omits it gets a 415
// which looks, from the user's side, like a rejected password.
//
// That is not hypothetical: login.html shipped without the header and every
// sign-in failed with "wrong username or password". The API tests all passed,
// because they set the header themselves. This test reads the pages instead.
func TestConsolePagesSendJSONContentType(t *testing.T) {
	// Matches a fetch() call up to its closing brace, non-greedily.
	call := regexp.MustCompile(`(?s)fetch\(.{0,600}?\}\s*\)`)
	method := regexp.MustCompile(`method\s*:\s*['"](\w+)['"]`)

	pages, err := staticFS.ReadDir("static")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, p := range pages {
		if !strings.HasSuffix(p.Name(), ".html") {
			continue
		}
		b, err := staticFS.ReadFile("static/" + p.Name())
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range call.FindAllString(string(b), -1) {
			m := method.FindStringSubmatch(c)
			if m == nil {
				continue // a plain GET
			}
			switch strings.ToUpper(m[1]) {
			case "POST", "PUT", "PATCH", "DELETE":
			default:
				continue
			}
			// A body-less call (logout) never reaches the decoder, so the header
			// is only required when something is actually sent.
			if !strings.Contains(c, "body") {
				continue
			}
			checked++
			if !strings.Contains(c, "Content-Type") {
				t.Errorf("%s: a %s fetch with a body does not set Content-Type:\n%s",
					p.Name(), m[1], c)
			}
		}
	}
	if checked == 0 {
		t.Fatal("found no write requests to check — the matcher has stopped working")
	}
	t.Logf("checked %d write requests across the console pages", checked)
}
