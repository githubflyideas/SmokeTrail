package main

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"mime"
	"net/http"
	"net/url"
	"strconv"
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
	exp  time.Time
}

func newSessions() *sessions { return &sessions{m: map[string]session{}} }

func (s *sessions) issue(user string) string {
	b := make([]byte, 32)
	rand.Read(b)
	tok := hex.EncodeToString(b)
	s.mu.Lock()
	s.m[tok] = session{user: user, exp: time.Now().Add(7 * 24 * time.Hour)}
	s.mu.Unlock()
	return tok
}

func (s *sessions) user(tok string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sn, ok := s.m[tok]
	if !ok || time.Now().After(sn.exp) {
		delete(s.m, tok)
		return "", false
	}
	return sn.user, true
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

const sessionCookie = "smoketrail_session"

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
	authed := func(r *http.Request) bool {
		_, ok := sess.user(token(r))
		return ok
	}
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
		h, err := hashPassword(body.Pass)
		if err != nil {
			jsonErr(w, http.StatusInternalServerError, "hash failed")
			return
		}
		if err := store.SetSetting(settingAdminUser, body.User); err != nil {
			jsonErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := store.SetSetting(settingAdminHash, h); err != nil {
			jsonErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		log.Printf("console: admin account %q created", body.User)
		setCookie(w, sess.issue(body.User), 7*24*3600)
		writeJSON(w, map[string]bool{"ok": true})
	})

	mux.HandleFunc("POST /api/login", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ User, Pass string }
		if !decodeJSON(w, r, &body) {
			return
		}
		user, _ := store.Setting(settingAdminUser)
		hash, ok := store.Setting(settingAdminHash)
		if !ok || body.User != user || !verifyPassword(hash, body.Pass) {
			// A fixed delay makes online guessing boring without needing a lockout
			// that an attacker could use to lock the operator out.
			time.Sleep(time.Second)
			log.Printf("console: failed login from %s", r.RemoteAddr)
			jsonErr(w, http.StatusUnauthorized, "wrong username or password")
			return
		}
		setCookie(w, sess.issue(user), 7*24*3600)
		writeJSON(w, map[string]bool{"ok": true})
	})

	mux.HandleFunc("POST /api/logout", func(w http.ResponseWriter, r *http.Request) {
		sess.drop(token(r))
		setCookie(w, "", -1)
		writeJSON(w, map[string]bool{"ok": true})
	})

	mux.HandleFunc("POST /api/password", write(func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Old, New string }
		if !decodeJSON(w, r, &body) {
			return
		}
		hash, _ := store.Setting(settingAdminHash)
		if !verifyPassword(hash, body.Old) {
			time.Sleep(time.Second)
			jsonErr(w, http.StatusUnauthorized, "current password is wrong")
			return
		}
		if err := validatePassword(body.New); err != nil {
			jsonErr(w, http.StatusBadRequest, err.Error())
			return
		}
		h, err := hashPassword(body.New)
		if err != nil {
			jsonErr(w, http.StatusInternalServerError, "hash failed")
			return
		}
		if err := store.SetSetting(settingAdminHash, h); err != nil {
			jsonErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		sess.dropAll()
		log.Printf("console: admin password changed; all sessions invalidated")
		writeJSON(w, map[string]bool{"ok": true})
	}))

	// ---- read ----

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

	mux.HandleFunc("POST /api/targets", write(func(w http.ResponseWriter, r *http.Request) {
		if t, ok := decodeTarget(w, r); ok {
			t, err := store.CreateTarget(t)
			done(w, t, err, "created")
		}
	}))
	mux.HandleFunc("PUT /api/targets/{id}", write(func(w http.ResponseWriter, r *http.Request) {
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
	mux.HandleFunc("DELETE /api/targets/{id}", write(func(w http.ResponseWriter, r *http.Request) {
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
