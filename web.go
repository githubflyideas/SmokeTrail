package main

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed static
var staticFS embed.FS

// sessions are in memory, so a restart logs everyone out. For a single-binary tool
// that is the right trade: no session table to migrate, no token to leak from disk,
// and a service restart is rare enough that re-login is not a burden.
type sessions struct {
	mu sync.Mutex
	m  map[string]session
}

type session struct {
	user string
	role string
	exp  time.Time
}

func newSessions() *sessions { return &sessions{m: map[string]session{}} }

func (s *sessions) issue(user, role string) string {
	b := make([]byte, 32)
	rand.Read(b)
	tok := hex.EncodeToString(b)
	s.mu.Lock()
	s.m[tok] = session{user: user, role: role, exp: time.Now().Add(7 * 24 * time.Hour)}
	s.mu.Unlock()
	return tok
}

func (s *sessions) get(tok string) (session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sn, ok := s.m[tok]
	if !ok || time.Now().After(sn.exp) {
		delete(s.m, tok)
		return session{}, false
	}
	return sn, true
}

// dropUser invalidates the sessions of one account — used when its password
// changes or it is deleted, so a cookie never outlives the credential behind it.
func (s *sessions) dropUser(name string) {
	s.mu.Lock()
	for tok, sn := range s.m {
		if sn.user == name {
			delete(s.m, tok)
		}
	}
	s.mu.Unlock()
}

func (s *sessions) drop(tok string) {
	s.mu.Lock()
	delete(s.m, tok)
	s.mu.Unlock()
}

// dropAll invalidates every session — used after a password change, so a stolen
// cookie does not outlive the password it was issued against.
func (s *sessions) dropAll() {
	s.mu.Lock()
	s.m = map[string]session{}
	s.mu.Unlock()
}

const sessionCookie = "pingping_session"

func newMux(cfg *Config, store *Store, run *Runner) http.Handler {
	sess := newSessions()
	mux := http.NewServeMux()

	token := func(r *http.Request) string {
		c, err := r.Cookie(sessionCookie)
		if err != nil {
			return ""
		}
		return c.Value
	}
	current := func(r *http.Request) (session, bool) { return sess.get(token(r)) }
	authed := func(r *http.Request) bool { _, ok := current(r); return ok }
	setCookie := func(w http.ResponseWriter, tok string, maxAge int) {
		http.SetCookie(w, &http.Cookie{
			Name: sessionCookie, Value: tok, Path: "/",
			HttpOnly: true, MaxAge: maxAge, SameSite: http.SameSiteLaxMode,
		})
	}

	// guard is the only gate. There is no anonymous read path: an open console
	// would expose which hosts this machine watches, which is itself a map of the
	// network.
	guard := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if !authed(r) {
				jsonErr(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			t0 := time.Now()
			h(w, r)
			if d := time.Since(t0); d > time.Second {
				log.Printf("slow request: %s %s took %v", r.URL.Path, r.URL.RawQuery, d)
			}
		}
	}
	// write additionally refuses cross-origin form posts. A JSON content type
	// forces a CORS preflight that we never answer, so a page on another site
	// cannot drive these endpoints with the operator's cookie.
	write := func(h http.HandlerFunc) http.HandlerFunc {
		return guard(func(w http.ResponseWriter, r *http.Request) {
			if !sameOrigin(r) {
				jsonErr(w, http.StatusForbidden, "cross-origin request refused")
				return
			}
			h(w, r)
		})
	}
	// admin wraps a write that changes what this host probes, or who may sign in.
	// A viewer reaching one gets 403 rather than 404: they are authenticated, and
	// pretending the endpoint does not exist would only make the UI confusing.
	admin := func(h http.HandlerFunc) http.HandlerFunc {
		return write(func(w http.ResponseWriter, r *http.Request) {
			// Two different refusals, in this order, because they mean different
			// things. --readonly is a property of the process and no credential
			// gets past it; the role check is a property of the account. Saying
			// "this account is read-only" to an admin on a --readonly instance
			// would send them looking for a permission to grant.
			if cfg.ReadOnly {
				jsonErr(w, http.StatusForbidden,
					"this instance was started with --readonly; targets cannot be changed from the console")
				return
			}
			sn, _ := current(r)
			if sn.role != RoleAdmin {
				jsonErr(w, http.StatusForbidden, "this account is read-only")
				return
			}
			h(w, r)
		})
	}
	page := func(name string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			b, _ := staticFS.ReadFile("static/" + name)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Referrer-Policy", "same-origin")
			w.Write(b)
		}
	}

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		if store.NeedsSetup() {
			http.Redirect(w, r, "/setup", http.StatusFound)
			return
		}
		if !authed(r) {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		page("index.html")(w, r)
	})
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		if store.NeedsSetup() {
			http.Redirect(w, r, "/setup", http.StatusFound)
			return
		}
		page("login.html")(w, r)
	})
	mux.HandleFunc("/setup", func(w http.ResponseWriter, r *http.Request) {
		if !store.NeedsSetup() {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		page("setup.html")(w, r)
	})
	mux.HandleFunc("/settings", func(w http.ResponseWriter, r *http.Request) {
		if store.NeedsSetup() {
			http.Redirect(w, r, "/setup", http.StatusFound)
			return
		}
		if !authed(r) {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		page("settings.html")(w, r)
	})
	mux.Handle("/static/", http.FileServer(http.FS(staticFS)))

	// ---- first run ----
	// Open until a password exists, closed forever after. The window is the same
	// one Technitium leaves open, and it is why the installer tells the operator to
	// open the console straight away.
	mux.HandleFunc("POST /api/setup", func(w http.ResponseWriter, r *http.Request) {
		if !store.NeedsSetup() {
			jsonErr(w, http.StatusConflict, "already set up")
			return
		}
		if !sameOrigin(r) {
			jsonErr(w, http.StatusForbidden, "cross-origin request refused")
			return
		}
		var body struct{ User, Pass string }
		if !decodeJSON(w, r, &body) {
			return
		}
		if body.User == "" {
			body.User = "admin"
		}
		if err := validateUsername(body.User); err != nil {
			jsonErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := validatePassword(body.Pass); err != nil {
			jsonErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := store.CreateUser(body.User, body.Pass, RoleAdmin); err != nil {
			jsonErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		log.Printf("console: admin account %q created", body.User)
		setCookie(w, sess.issue(body.User, RoleAdmin), 7*24*3600)
		writeJSON(w, map[string]bool{"ok": true})
	})

	mux.HandleFunc("POST /api/login", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ User, Pass string }
		if !decodeJSON(w, r, &body) {
			return
		}
		role, ok := store.Authenticate(body.User, body.Pass)
		if !ok {
			// A fixed delay makes online guessing boring without needing a lockout
			// that an attacker could use to lock the operator out.
			time.Sleep(time.Second)
			log.Printf("console: failed login for %q from %s", body.User, r.RemoteAddr)
			jsonErr(w, http.StatusUnauthorized, "wrong username or password")
			return
		}
		setCookie(w, sess.issue(body.User, role), 7*24*3600)
		writeJSON(w, map[string]bool{"ok": true})
	})

	mux.HandleFunc("POST /api/logout", func(w http.ResponseWriter, r *http.Request) {
		sess.drop(token(r))
		setCookie(w, "", -1)
		writeJSON(w, map[string]bool{"ok": true})
	})

	// Changing your own password needs the old one. Viewers may do this too: it is
	// their account.
	mux.HandleFunc("POST /api/password", write(func(w http.ResponseWriter, r *http.Request) {
		sn, _ := current(r)
		var body struct{ Old, New string }
		if !decodeJSON(w, r, &body) {
			return
		}
		if _, ok := store.Authenticate(sn.user, body.Old); !ok {
			time.Sleep(time.Second)
			jsonErr(w, http.StatusUnauthorized, "current password is wrong")
			return
		}
		if err := store.SetPassword(sn.user, body.New); err != nil {
			jsonErr(w, http.StatusBadRequest, err.Error())
			return
		}
		sess.dropUser(sn.user)
		log.Printf("console: %q changed their password; their sessions were invalidated", sn.user)
		writeJSON(w, map[string]bool{"ok": true})
	}))

	// ---- accounts and settings ----

	// Who am I, and what may I do. The console asks this first and hides what the
	// answer says is unavailable — the server still enforces it, the UI just stops
	// offering buttons that would 403.
	mux.HandleFunc("GET /api/me", guard(func(w http.ResponseWriter, r *http.Request) {
		sn, _ := current(r)
		writeJSON(w, map[string]any{
			"user":           sn.user,
			"role":           sn.role,
			"version":        version,
			"data_dir":       cfg.DataDir,
			"listen":         cfg.Listen,
			"port":           portNum(cfg.Listen),
			"retention_days": cfg.RetentionDays,
			"readonly":       cfg.ReadOnly,
		})
	}))

	mux.HandleFunc("GET /api/users", guard(func(w http.ResponseWriter, r *http.Request) {
		if sn, _ := current(r); sn.role != RoleAdmin {
			jsonErr(w, http.StatusForbidden, "this account is read-only")
			return
		}
		us, err := store.Users()
		if err != nil {
			jsonErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if us == nil {
			us = []User{}
		}
		writeJSON(w, us)
	}))

	userErr := func(w http.ResponseWriter, err error) bool {
		switch {
		case err == nil:
			return false
		case errors.Is(err, errUserExists), errors.Is(err, errLastAdmin):
			jsonErr(w, http.StatusConflict, err.Error())
		case errors.Is(err, errUserNotFound):
			jsonErr(w, http.StatusNotFound, err.Error())
		default:
			jsonErr(w, http.StatusBadRequest, err.Error())
		}
		return true
	}

	mux.HandleFunc("POST /api/users", admin(func(w http.ResponseWriter, r *http.Request) {
		var b struct{ Name, Pass, Role string }
		if !decodeJSON(w, r, &b) {
			return
		}
		if b.Role == "" {
			b.Role = RoleViewer // the safe default: looking, not changing
		}
		if userErr(w, store.CreateUser(b.Name, b.Pass, b.Role)) {
			return
		}
		log.Printf("console: account %q created with role %s", b.Name, b.Role)
		writeJSON(w, map[string]bool{"ok": true})
	}))

	mux.HandleFunc("POST /api/users/{name}/password", admin(func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		var b struct{ Pass string }
		if !decodeJSON(w, r, &b) {
			return
		}
		if userErr(w, store.SetPassword(name, b.Pass)) {
			return
		}
		sess.dropUser(name)
		log.Printf("console: password reset for %q; their sessions were invalidated", name)
		writeJSON(w, map[string]bool{"ok": true})
	}))

	mux.HandleFunc("POST /api/users/{name}/role", admin(func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		var b struct{ Role string }
		if !decodeJSON(w, r, &b) {
			return
		}
		if userErr(w, store.SetRole(name, b.Role)) {
			return
		}
		// The role is carried in the session, so it has to be re-issued.
		sess.dropUser(name)
		log.Printf("console: %q is now %s", name, b.Role)
		writeJSON(w, map[string]bool{"ok": true})
	}))

	mux.HandleFunc("DELETE /api/users/{name}", admin(func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if sn, _ := current(r); name == sn.user {
			jsonErr(w, http.StatusConflict, "you cannot delete the account you are signed in with")
			return
		}
		if userErr(w, store.DeleteUser(name)) {
			return
		}
		sess.dropUser(name)
		log.Printf("console: account %q deleted", name)
		writeJSON(w, map[string]bool{"ok": true})
	}))

	// The console port is a stored setting, not a service argument, so it can be
	// changed here. It cannot take effect until the listener is rebuilt, and
	// rebuilding it underneath the request that asked would drop that request — so
	// this records the intent and the answer says what to do next.
	mux.HandleFunc("POST /api/settings/port", admin(func(w http.ResponseWriter, r *http.Request) {
		var b struct{ Port int }
		if !decodeJSON(w, r, &b) {
			return
		}
		if err := store.SetConsolePort(b.Port); err != nil {
			jsonErr(w, http.StatusBadRequest, err.Error())
			return
		}
		log.Printf("console: port will be %d after a restart", b.Port)
		writeJSON(w, map[string]any{
			"ok":      true,
			"port":    b.Port,
			"applied": false,
			"note":    "Restart the pingping service to apply it — the tray icon can do that, and it fixes the firewall rule at the same time.",
		})
	}))

	// ---- read ----

	// Health is what the notification-area icon polls. It is unauthenticated,
	// because the tray has no session and cannot get one — so it is restricted to
	// loopback and carries counts only. No hostnames: the list of what a machine
	// watches is a map of the network, and that stays behind the login.
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		if !isLoopback(r) {
			http.NotFound(w, r)
			return
		}
		targets := run.Targets()
		down := 0
		since := time.Now().Add(-time.Hour).Unix()
		for _, t := range targets {
			if rec := store.Recent(t.Name, since); len(rec) > 0 && rec[len(rec)-1].R == 0 {
				down++
			}
		}
		// Both ports, so the tray can follow a change it did not make. It polls
		// the effective one; if configured differs, a restart is pending and the
		// tray knows where to look afterwards.
		configured := store.ConsolePort()
		if configured == 0 {
			configured = portNum(cfg.Listen)
		}
		writeJSON(w, map[string]any{
			"version":         version,
			"needs_setup":     store.NeedsSetup(),
			"targets":         len(targets),
			"down":            down,
			"port":            portNum(cfg.Listen),
			"port_configured": configured,
		})
	})

	// The catalogue, one language at a time. Unauthenticated on purpose: the sign-in
	// and first-run screens need it before anyone has a session, and it contains
	// nothing but interface text.
	mux.HandleFunc("GET /api/i18n", func(w http.ResponseWriter, r *http.Request) {
		lang := normalizeLang(r.URL.Query().Get("lang"))
		if lang == "" {
			lang = acceptLanguage(r.Header.Get("Accept-Language"))
		}
		w.Header().Set("Cache-Control", "no-cache")
		writeJSON(w, bundle(lang))
	})

	mux.HandleFunc("/api/version", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"version":        version,
			"retention_days": cfg.RetentionDays,
			"needs_setup":    store.NeedsSetup(),
			"authed":         authed(r),
		})
	})

	mux.HandleFunc("GET /api/targets", guard(func(w http.ResponseWriter, r *http.Request) {
		now := time.Now()
		type item struct {
			ID          int64  `json:"id"`
			Name        string `json:"name"`
			Type        string `json:"type"`
			Host        string `json:"host"`
			Port        int    `json:"port"`
			Addr        string `json:"addr"`
			IntervalSec int    `json:"interval_sec"` // effective
			IntervalSet int    `json:"interval_set"` // explicit override, 0 = from pace
			Pace        string `json:"pace"`
			Down        bool   `json:"down"`
			Last1h      Stats  `json:"last_1h"`
			Last24h     Stats  `json:"last_24h"`
		}
		out := []item{}
		for _, t := range run.Targets() {
			iv, _ := probeParams(t, cfg.Probe)
			pace := t.Pace
			if pace == "" {
				pace = "normal"
			}
			rec := store.Recent(t.Name, now.Add(-time.Hour).Unix())
			down := len(rec) > 0 && rec[len(rec)-1].R == 0
			out = append(out, item{
				ID: t.ID, Name: t.Name, Type: t.Type, Host: t.Host, Port: t.Port, Addr: targetAddr(t),
				IntervalSec: int(iv.Seconds()), IntervalSet: t.IntervalSec, Pace: pace, Down: down,
				Last1h:  calcStats(rec),
				Last24h: calcStats(store.Recent(t.Name, now.Add(-24*time.Hour).Unix())),
			})
		}
		writeJSON(w, out)
	}))

	// raw rounds for smoke. Supports either minutes=N (recent window) or from/to unix
	// (arbitrary range). No thinning: every sample is returned as stored.
	mux.HandleFunc("GET /api/series", guard(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		name := q.Get("target")
		from, _ := strconv.ParseInt(q.Get("from"), 10, 64)
		to, _ := strconv.ParseInt(q.Get("to"), 10, 64)
		if from == 0 || to == 0 {
			minutes, _ := strconv.Atoi(q.Get("minutes"))
			if minutes <= 0 || minutes > 432000 { // up to 300 days
				minutes = 360
			}
			to = time.Now().Unix()
			from = to - int64(minutes)*60
		}
		writeJSON(w, store.ReadRange(r.Context(), name, from, to))
	}))

	// ---- target CRUD ----
	// Every write goes to the database first, then Runner.Reload picks it up, so a
	// half-applied change cannot exist: either the row is there and probing, or it
	// is not there at all.

	targetID := func(w http.ResponseWriter, r *http.Request) (int64, bool) {
		n, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || n <= 0 {
			jsonErr(w, http.StatusBadRequest, "bad id")
			return 0, false
		}
		return n, true
	}
	decodeTarget := func(w http.ResponseWriter, r *http.Request) (TargetCfg, bool) {
		var t TargetCfg
		if !decodeJSON(w, r, &t) {
			return t, false
		}
		if err := normalizeTarget(&t); err != nil {
			jsonErr(w, http.StatusBadRequest, err.Error())
			return t, false
		}
		return t, true
	}
	done := func(w http.ResponseWriter, t TargetCfg, err error, verb string) {
		switch {
		case errors.Is(err, errNameTaken), errors.Is(err, errNameDeleted):
			jsonErr(w, http.StatusConflict, err.Error())
		case errors.Is(err, errNotFound):
			jsonErr(w, http.StatusNotFound, err.Error())
		case err != nil:
			log.Printf("target %s: %v", verb, err)
			jsonErr(w, http.StatusInternalServerError, err.Error())
		default:
			if err := run.Reload(); err != nil {
				log.Printf("reload after %s: %v", verb, err)
			}
			log.Printf("console: target %s %q (%s)", verb, t.Name, targetAddr(t))
			writeJSON(w, t)
		}
	}

	mux.HandleFunc("POST /api/targets", admin(func(w http.ResponseWriter, r *http.Request) {
		if t, ok := decodeTarget(w, r); ok {
			t, err := store.CreateTarget(t)
			done(w, t, err, "created")
		}
	}))
	mux.HandleFunc("PUT /api/targets/{id}", admin(func(w http.ResponseWriter, r *http.Request) {
		id, ok := targetID(w, r)
		if !ok {
			return
		}
		if t, ok := decodeTarget(w, r); ok {
			t.ID = id
			t, err := store.UpdateTarget(t)
			done(w, t, err, "updated")
		}
	}))
	mux.HandleFunc("DELETE /api/targets/{id}", admin(func(w http.ResponseWriter, r *http.Request) {
		id, ok := targetID(w, r)
		if !ok {
			return
		}
		var t TargetCfg
		for _, x := range run.Targets() {
			if x.ID == id {
				t = x
			}
		}
		done(w, t, store.DeactivateTarget(id), "deleted")
	}))

	return mux
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
		jsonErr(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
		return false
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(v); err != nil {
		jsonErr(w, http.StatusBadRequest, "bad JSON: "+err.Error())
		return false
	}
	return true
}

// acceptLanguage picks the best catalogue match from a browser's header. Quality
// values are honoured because a browser set to "ja, en;q=0.8" means it, and
// ignoring q would hand that user English.
func acceptLanguage(h string) string {
	type cand struct {
		tag string
		q   float64
	}
	var best cand
	for _, part := range strings.Split(h, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		tag, q := part, 1.0
		if i := strings.Index(part, ";"); i >= 0 {
			tag = strings.TrimSpace(part[:i])
			if _, err := fmt.Sscanf(strings.TrimSpace(part[i+1:]), "q=%f", &q); err != nil {
				q = 1.0
			}
		}
		if l := normalizeLang(tag); l != "" && q > best.q-0.0001 && (best.tag == "" || q > best.q) {
			best = cand{l, q}
		}
	}
	if best.tag == "" {
		return langEN
	}
	return best.tag
}

// portNum extracts the numeric port from a listen address.
func portNum(listen string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(portOf(listen), ":"))
	return n
}

// isLoopback reports whether the request came from this machine. Used only to
// scope the unauthenticated health endpoint; everything else goes through auth.
func isLoopback(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// sameOrigin rejects browser requests whose Origin doesn't match the Host they were
// sent to. Non-browser clients (curl) send no Origin and are let through to auth.
func sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	u, err := url.Parse(o)
	return err == nil && u.Host == r.Host
}

func jsonErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(v)
}
