package main

import (
	"crypto/sha256"
	"encoding/base64"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"sync"
)

// Everything the console serves — the four pages, the translations, the chart
// library — is compiled into the binary, so a file's bytes change only when the
// binary does. Yet none of those responses carried a cache header, and a
// response with no Cache-Control, no ETag and no Last-Modified is one a browser
// may keep for as long as it likes: RFC 9111 §4.2.2 lets it invent a lifetime
// when the server gives it nothing to go on, and they all do.
//
// The consequence is worse than a stale page, because it is silent and it looks
// like something else. Upgrade the service, reload the console, and the browser
// serves the settings page from the version that was just replaced: new binary,
// old markup, and a fix that appears to have done nothing. That is exactly what
// happened to the logo picker — the widened file filter shipped in 0.5.4 and the
// browser kept handing back the 0.5.3 page, so the report came back as "still
// broken" and the next thing looked at was the code that was already fixed.
//
// An embedded file has a validator built in: the hash of its own contents. So
// every asset gets an ETag computed once at startup, and `no-cache`, which does
// not mean "do not store" — it means "store it, but ask me before reusing it".
// A browser then revalidates, the server answers 304 with no body, and the 1 MB
// chart library costs one conditional request per load rather than a megabyte.
// When the binary changes the hash changes with it, the 304 becomes a 200, and
// nobody has to be told to clear their cache.
type assets struct {
	fs   fs.FS
	etag map[string]string // path inside the embedded tree → quoted ETag
}

// embeddedAssets hashes the tree on first use. Tests build several muxes in one
// process and there is no reason to hash six files once per mux.
var embeddedAssets = sync.OnceValue(func() *assets { return newAssets(staticFS) })

func newAssets(fsys fs.FS) *assets {
	a := &assets{fs: fsys, etag: map[string]string{}}
	fs.WalkDir(fsys, "static", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		b, err := fs.ReadFile(fsys, p)
		if err != nil {
			return nil
		}
		sum := sha256.Sum256(b)
		// 12 bytes is 96 bits of a SHA-256. An ETag only has to differ between
		// two versions of one file; it is not a signature.
		a.etag[p] = `"` + base64.RawURLEncoding.EncodeToString(sum[:12]) + `"`
		return nil
	})
	return a
}

// serve writes one embedded file with its validator. name is the path inside the
// embedded tree, so "static/settings.html". An empty contentType is taken from
// the extension.
func (a *assets) serve(w http.ResponseWriter, r *http.Request, name, contentType string) {
	b, err := fs.ReadFile(a.fs, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if contentType == "" {
		if contentType = mime.TypeByExtension(path.Ext(name)); contentType == "" {
			contentType = "application/octet-stream"
		}
	}
	h := w.Header()
	h.Set("Content-Type", contentType)
	h.Set("X-Content-Type-Options", "nosniff")
	if tag := a.etag[name]; tag != "" {
		h.Set("ETag", tag)
		h.Set("Cache-Control", "no-cache")
		if etagMatches(r.Header.Get("If-None-Match"), tag) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	} else {
		// No validator, so the only safe answer is to forbid reuse outright.
		h.Set("Cache-Control", "no-store")
	}
	if r.Method == http.MethodHead {
		return
	}
	w.Write(b)
}

// If-None-Match carries a list rather than a single tag, and anything in the
// path is allowed to weaken a tag to W/"…" on the way through.
func etagMatches(header, tag string) bool {
	if header == "" {
		return false
	}
	for _, c := range strings.Split(header, ",") {
		c = strings.TrimSpace(c)
		if c == "*" || strings.TrimPrefix(c, "W/") == tag {
			return true
		}
	}
	return false
}

// assetPath turns a request path into a path inside the embedded tree, or "" if
// it points outside it. Cleaning against a leading slash is what stops `..`:
// path.Clean resolves the segments first, so there is nothing left to escape
// with by the time the prefix is checked.
func assetPath(urlPath string) string {
	p := strings.TrimPrefix(path.Clean("/"+urlPath), "/")
	if p != "static" && !strings.HasPrefix(p, "static/") {
		return ""
	}
	return p
}
