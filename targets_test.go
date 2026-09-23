package main

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A 1.0.x database has a name-only targets table and rounds keyed by its ids.
// The rows must come out inactive, and adding the same name in the web UI must
// land on the same id — i.e. bring the history back.
func TestMigrationKeepsHistory(t *testing.T) {
	dir := t.TempDir()
	db, _ := sql.Open(sqlDriver, filepath.Join(dir, "pingping.db"))
	db.Exec(`CREATE TABLE targets (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL UNIQUE)`)
	db.Exec(`INSERT INTO targets(name) VALUES('HK CN2')`)
	db.Close()

	s, err := NewStore(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if ts, _ := s.ActiveTargets(); len(ts) != 0 {
		t.Fatalf("legacy rows must come out inactive, got %+v", ts)
	}
	got, err := s.CreateTarget(TargetCfg{Name: "HK CN2", Type: "icmp", Host: "59.43.247.1", Pace: "fast"})
	if err != nil || got.ID != 1 {
		t.Fatalf("re-adding the old name should reactivate id 1, got %+v %v", got, err)
	}
}

func TestTargetCRUDSemantics(t *testing.T) {
	s, _ := NewStore(t.TempDir(), nil)
	defer s.Close()
	a, err := s.CreateTarget(TargetCfg{Name: "a", Type: "icmp", Host: "192.0.2.1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTarget(TargetCfg{Name: "a", Type: "icmp", Host: "192.0.2.9"}); err != errNameTaken {
		t.Fatalf("create over an active name must conflict, got %v", err)
	}
	b, _ := s.CreateTarget(TargetCfg{Name: "b", Type: "icmp", Host: "192.0.2.2"})
	b.Name = "a"
	if _, err := s.UpdateTarget(b); err != errNameTaken {
		t.Fatalf("rename onto an existing name must conflict, got %v", err)
	}
	a.Name = "a-renamed"
	if _, err := s.UpdateTarget(a); err != nil {
		t.Fatal(err)
	}
	if err := s.DeactivateTarget(a.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeactivateTarget(a.ID); err != errNotFound {
		t.Fatalf("double delete: %v", err)
	}
	again, err := s.CreateTarget(TargetCfg{Name: "a-renamed", Type: "tcp", Host: "192.0.2.3", Port: 22})
	if err != nil || again.ID != a.ID {
		t.Fatalf("re-adding a deleted name should reactivate id %d, got %+v %v", a.ID, again, err)
	}
}

func TestRunnerRenameKeepsRing(t *testing.T) {
	s, _ := NewStore(t.TempDir(), nil)
	defer s.Close()
	tg, _ := s.CreateTarget(TargetCfg{Name: "old", Type: "tcp", Host: "127.0.0.1", Port: 1, IntervalSec: 3600})
	r := NewRunner(ProbeCfg{Packets: 1, TimeoutMs: 50}, s, NewDetector(s))
	r.Reload()
	time.Sleep(200 * time.Millisecond) // first round runs immediately
	tg.Name = "new"
	s.UpdateTarget(tg)
	r.Reload()
	if len(s.Recent("new", 0)) == 0 {
		t.Fatal("rename dropped the in-memory ring")
	}
	if got := r.Targets(); len(got) != 1 || got[0].Name != "new" {
		t.Fatalf("runner state: %+v", got)
	}
}

func TestWebWriteGates(t *testing.T) {
	s, _ := NewStore(t.TempDir(), nil)
	defer s.Close()
	run := NewRunner(ProbeCfg{Packets: 1, TimeoutMs: 50}, s, NewDetector(s))
	h := newMux(&Config{}, s, run)
	body := `{"type":"tcp","host":"127.0.0.1","port":1,"interval_sec":3600}`

	post := func(path, ct, origin, payload, cookie string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "http://st.local"+path, strings.NewReader(payload))
		if ct != "" {
			req.Header.Set("Content-Type", ct)
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		if cookie != "" {
			req.Header.Set("Cookie", sessionCookie+"="+cookie)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	sessionOf := func(w *httptest.ResponseRecorder) string {
		for _, c := range w.Result().Cookies() {
			if c.Name == sessionCookie {
				return c.Value
			}
		}
		return ""
	}

	// Before setup there is no admin, and no anonymous write path either.
	if w := post("/api/targets", "application/json", "http://st.local", body, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("write without a session must be 401, got %d", w.Code)
	}

	// First run creates the account and logs the browser straight in.
	w := post("/api/setup", "application/json", "http://st.local", `{"User":"admin","Pass":"correct horse battery"}`, "")
	if w.Code != http.StatusOK {
		t.Fatalf("setup failed: %d %s", w.Code, w.Body)
	}
	tok := sessionOf(w)
	if tok == "" {
		t.Fatal("setup issued no session cookie")
	}
	if s.NeedsSetup() {
		t.Fatal("store still reports NeedsSetup after setup")
	}
	// That window closes permanently.
	if w := post("/api/setup", "application/json", "http://st.local", `{"User":"x","Pass":"another password"}`, ""); w.Code != http.StatusConflict {
		t.Fatalf("second setup must be 409, got %d", w.Code)
	}

	if w := post("/api/targets", "text/plain", "http://st.local", body, tok); w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("non-JSON body must be refused (CSRF), got %d", w.Code)
	}
	if w := post("/api/targets", "application/json", "http://evil.example", body, tok); w.Code != http.StatusForbidden {
		t.Fatalf("cross-origin must be refused, got %d", w.Code)
	}
	if w := post("/api/targets", "application/json", "http://st.local", body, "deadbeef"); w.Code != http.StatusUnauthorized {
		t.Fatalf("bogus session must be 401, got %d", w.Code)
	}
	if w := post("/api/targets", "application/json", "http://st.local", body, tok); w.Code != http.StatusOK {
		t.Fatalf("authenticated same-origin create failed: %d %s", w.Code, w.Body)
	}
	if got := run.Targets(); len(got) != 1 || got[0].Name != "127.0.0.1:1" {
		t.Fatalf("created target not running: %+v", got)
	}
	if w := post("/api/targets", "application/json", "http://st.local", body, tok); w.Code != http.StatusConflict {
		t.Fatalf("duplicate create should 409, got %d", w.Code)
	}

	// A password change must not leave old cookies usable.
	if w := post("/api/password", "application/json", "http://st.local",
		`{"Old":"correct horse battery","New":"a different long one"}`, tok); w.Code != http.StatusOK {
		t.Fatalf("password change failed: %d %s", w.Code, w.Body)
	}
	if w := post("/api/targets", "application/json", "http://st.local", body, tok); w.Code != http.StatusUnauthorized {
		t.Fatalf("session must die with the password it was issued against, got %d", w.Code)
	}
	if w := post("/api/login", "application/json", "http://st.local",
		`{"User":"admin","Pass":"a different long one"}`, ""); w.Code != http.StatusOK {
		t.Fatalf("login with the new password failed: %d %s", w.Code, w.Body)
	}
}
