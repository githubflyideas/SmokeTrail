package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// A console page with no cache header is a fix that does not ship. The logo
// picker was widened in 0.5.4 and the operator's browser went on serving the
// 0.5.3 markup, because the response carried no Cache-Control, no ETag and no
// Last-Modified, and a browser given no validator picks its own lifetime.
//
// Every asset is embedded in the binary, so its hash is a validator and there is
// no excuse for shipping without one. These tests check the three things that
// make an upgrade visible: a validator is always present, it is honoured, and it
// changes when the bytes do.

func TestEmbeddedAssetsCarryAValidator(t *testing.T) {
	as := embeddedAssets()

	for _, name := range []string{
		"static/index.html", "static/settings.html", "static/login.html",
		"static/setup.html", "static/i18n.js", "static/echarts.min.js",
	} {
		rec := httptest.NewRecorder()
		as.serve(rec, httptest.NewRequest("GET", "/", nil), name, "")

		if rec.Code != 200 {
			t.Errorf("%s: status %d", name, rec.Code)
			continue
		}
		if got := rec.Header().Get("ETag"); got == "" {
			t.Errorf("%s: no ETag, so a browser may serve it from cache after an upgrade", name)
		}
		if got := rec.Header().Get("Cache-Control"); got == "" {
			t.Errorf("%s: no Cache-Control, so the browser invents a lifetime for it", name)
		}
	}
}

// The pages the console actually routes to, through the real mux, because the
// handler that serves them has been rewritten once already and the wiring is
// what broke last time — not the serving.
func TestConsolePagesAreRevalidated(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h := newMux(&Config{}, s, NewRunner(ProbeCfg{}, s, NewDetector(s)))

	// /login and /setup need no session. Which of the two answers 200 depends on
	// whether a password exists, so both are accepted and one must be a page.
	for _, path := range []string{"/login", "/setup", "/static/i18n.js"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code == http.StatusFound {
			continue // redirected to the other one; its turn comes below
		}
		if rec.Code != 200 {
			t.Errorf("GET %s: status %d", path, rec.Code)
			continue
		}
		tag := rec.Header().Get("ETag")
		if tag == "" {
			t.Errorf("GET %s: no ETag", path)
			continue
		}
		if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" && cc != "no-store" {
			t.Errorf("GET %s: Cache-Control is %q; a cached copy of this page outlives an upgrade", path, cc)
		}

		// The validator has to be honoured, or every reload costs a full body and
		// the 1 MB chart library is sent again on every page load.
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("If-None-Match", tag)
		again := httptest.NewRecorder()
		h.ServeHTTP(again, req)
		if again.Code != http.StatusNotModified {
			t.Errorf("GET %s with If-None-Match: status %d, want 304", path, again.Code)
		}
		if n := again.Body.Len(); n != 0 {
			t.Errorf("GET %s: 304 carried a %d-byte body", path, n)
		}
	}
}

// The whole point of a content hash: change the file, and the tag no longer
// matches, so the browser is told to fetch it again. A tag derived from anything
// else — a build date, a version string, a constant — would let an edit ship
// invisibly, which is the bug this replaced.
func TestValidatorFollowsTheContent(t *testing.T) {
	before := newAssets(fstest.MapFS{"static/x.html": {Data: []byte("<p>old")}})
	after := newAssets(fstest.MapFS{"static/x.html": {Data: []byte("<p>new")}})

	a, b := before.etag["static/x.html"], after.etag["static/x.html"]
	if a == "" || b == "" {
		t.Fatal("no tag was computed")
	}
	if a == b {
		t.Fatal("the same tag for different content: an upgraded page would never be fetched")
	}

	// And a request holding the old tag must get the new bytes, not a 304.
	req := httptest.NewRequest("GET", "/static/x.html", nil)
	req.Header.Set("If-None-Match", a)
	rec := httptest.NewRecorder()
	after.serve(rec, req, "static/x.html", "")
	if rec.Code != 200 || rec.Body.String() != "<p>new" {
		t.Fatalf("got %d %q, want 200 and the new content", rec.Code, rec.Body.String())
	}
}

func TestEtagMatchesHandlesAList(t *testing.T) {
	const tag = `"abc"`
	for _, c := range []struct {
		header string
		want   bool
	}{
		{``, false},
		{tag, true},
		{`W/` + tag, true},   // weakened in transit
		{`*`, true},          // any representation will do
		{`"z", "abc"`, true}, // a list, which is the normal shape
		{`"z"`, false},
	} {
		if got := etagMatches(c.header, tag); got != c.want {
			t.Errorf("etagMatches(%q) = %v, want %v", c.header, got, c.want)
		}
	}
}

// Serving files by request path means the path decides which file, so it has to
// be unable to name one outside the tree. This replaced http.FileServer, which
// did that cleaning itself.
func TestAssetPathCannotEscape(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"/static/i18n.js", "static/i18n.js"},
		{"/static/./i18n.js", "static/i18n.js"},
		{"/static/sub/../i18n.js", "static/i18n.js"},
		{"/static/../brand.go", ""},
		{"/static/../../etc/passwd", ""},
		{"/etc/passwd", ""},
		{"/staticfoo/x", ""},
	} {
		if got := assetPath(c.in); got != c.want {
			t.Errorf("assetPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// The upload is gated by the bytes, not by the picker, so the page must not carry
// a filter that can hide the file someone came for. Two shapes of `accept` have
// now done exactly that on Windows, and each time it read as the upload being
// broken rather than the dialog being wrong.
func TestLogoPickerHasNoFilter(t *testing.T) {
	src := readSource(t, "static/settings.html")

	i := strings.Index(src, `id="brandFile"`)
	if i < 0 {
		t.Fatal("the logo file input is gone from settings.html")
	}
	// Walk back to the "<" that opens the tag, then forward to its ">".
	start := strings.LastIndex(src[:i], "<")
	tag := src[start:]
	if end := strings.Index(tag, ">"); end > 0 {
		tag = tag[:end+1]
	}
	if strings.Contains(strings.ToLower(tag), "accept") {
		t.Errorf("the logo picker declares a filter (%s); the server sniffs the "+
			"content anyway, and a filter that guesses wrong shows an empty folder", tag)
	}
}
