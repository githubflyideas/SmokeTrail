package main

import (
	_ "embed"
	"encoding/json"
	"log"
	"sort"
	"strings"
)

// One catalogue, shared by the notification-area icon and the web console.
//
// It lives in JSON rather than in Go source because two very different consumers
// read it — a Win32 menu and a browser — and a translator should be able to work
// on one file without touching either. The Go side still projects it into a
// struct (below), so every call site is compile-checked and a typo in a key
// cannot reach a user as a rendered key name; the completeness test catches the
// other half, a key that exists but has no translation.
//
//go:embed i18n/strings.json
var i18nRaw []byte

type catalogue struct {
	Langs   []string                     `json:"langs"`
	Names   map[string]string            `json:"names"`
	Strings map[string]map[string]string `json:"strings"`
}

var cat catalogue

func init() {
	if err := json.Unmarshal(i18nRaw, &cat); err != nil {
		// Embedded and tested; if this ever fails the binary is not shippable.
		log.Fatalf("i18n: %v", err)
	}
}

const (
	langAuto = ""
	langEN   = "en"
)

// tr looks up one string, falling back to English and then to the key itself —
// a visible key is ugly but it is better than a blank menu item, and the test
// suite means it should never happen in a release.
func tr(lang, key string) string {
	m, ok := cat.Strings[key]
	if !ok {
		return key
	}
	if v := m[lang]; v != "" {
		return v
	}
	if v := m[langEN]; v != "" {
		return v
	}
	return key
}

func knownLang(tag string) bool {
	for _, l := range cat.Langs {
		if l == tag {
			return true
		}
	}
	return false
}

// normalizeLang maps a browser or OS tag ("zh-Hans-CN", "pt_BR") onto one we have.
func normalizeLang(tag string) string {
	tag = strings.ToLower(strings.TrimSpace(tag))
	tag = strings.ReplaceAll(tag, "_", "-")
	if knownLang(tag) {
		return tag
	}
	if i := strings.Index(tag, "-"); i > 0 {
		if base := tag[:i]; knownLang(base) {
			return base
		}
	}
	return ""
}

// msgs is the projection the notification-area icon uses. Keeping it a struct
// means a renamed key breaks the build here rather than blanking a menu item on
// somebody's desktop.
type msgs struct {
	OpenConsole    string
	SettingsItem   string
	DataFolder     string
	RestartService string
	StopService    string
	StartService   string
	CheckUpdates   string
	AboutItem      string
	ExitItem       string
	CloseIconItem  string
	QuitAllItem    string
	LanguageItem   string
	LangAuto       string

	Starting      string
	NotResponding string
	NeedsSetup    string
	NoTargets     string
	AllUpFmt      string // one %d: target count
	SomeDownFmt   string // two %d: target count, down count

	StoppedTitle   string
	StoppedBodyFmt string // one %s: console URL
	BackTitle      string
	DownTitle      string
	DownBodyFmt    string // two %d: down count, target count
	UpTitle        string
	UpBodyFmt      string // one %d: target count

	AboutTitle   string
	AboutTagline string
	AboutConsole string
	AboutStatus  string
	AboutData    string
	AboutLicense string
}

// trayKeys is the map from struct field to catalogue key, in one place so the
// completeness test can walk it.
var trayKeys = map[string]string{
	"OpenConsole": "tray.openConsole", "SettingsItem": "tray.settings",
	"DataFolder": "tray.dataFolder", "RestartService": "tray.restartService",
	"StopService": "tray.stopService", "StartService": "tray.startService",
	"CheckUpdates": "tray.checkUpdates", "AboutItem": "tray.about",
	"ExitItem": "tray.exit", "CloseIconItem": "tray.closeIcon", "QuitAllItem": "tray.quitAll", "LanguageItem": "tray.language",
	"LangAuto": "tray.langAuto",

	"Starting": "status.starting", "NotResponding": "status.notResponding",
	"NeedsSetup": "status.needsSetup", "NoTargets": "status.noTargets",
	"AllUpFmt": "status.allUp", "SomeDownFmt": "status.someDown",

	"StoppedTitle": "notify.stoppedTitle", "StoppedBodyFmt": "notify.stoppedBody",
	"BackTitle": "notify.backTitle", "DownTitle": "notify.downTitle",
	"DownBodyFmt": "notify.downBody", "UpTitle": "notify.upTitle",
	"UpBodyFmt": "notify.upBody",

	"AboutTitle": "tray.about", "AboutTagline": "about.tagline",
	"AboutConsole": "about.console", "AboutStatus": "about.status",
	"AboutData": "about.data", "AboutLicense": "settings.license",
}

func messages(lang string) msgs {
	t := func(k string) string { return tr(lang, k) }
	return msgs{
		OpenConsole:    t(trayKeys["OpenConsole"]),
		SettingsItem:   t(trayKeys["SettingsItem"]),
		DataFolder:     t(trayKeys["DataFolder"]),
		RestartService: t(trayKeys["RestartService"]),
		StopService:    t(trayKeys["StopService"]),
		StartService:   t(trayKeys["StartService"]),
		CheckUpdates:   t(trayKeys["CheckUpdates"]),
		AboutItem:      t(trayKeys["AboutItem"]),
		ExitItem:       t(trayKeys["ExitItem"]),
		CloseIconItem:  t(trayKeys["CloseIconItem"]),
		QuitAllItem:    t(trayKeys["QuitAllItem"]),
		LanguageItem:   t(trayKeys["LanguageItem"]),
		LangAuto:       t(trayKeys["LangAuto"]),

		Starting:      t(trayKeys["Starting"]),
		NotResponding: t(trayKeys["NotResponding"]),
		NeedsSetup:    t(trayKeys["NeedsSetup"]),
		NoTargets:     t(trayKeys["NoTargets"]),
		AllUpFmt:      t(trayKeys["AllUpFmt"]),
		SomeDownFmt:   t(trayKeys["SomeDownFmt"]),

		StoppedTitle:   t(trayKeys["StoppedTitle"]),
		StoppedBodyFmt: t(trayKeys["StoppedBodyFmt"]),
		BackTitle:      t(trayKeys["BackTitle"]),
		DownTitle:      t(trayKeys["DownTitle"]),
		DownBodyFmt:    t(trayKeys["DownBodyFmt"]),
		UpTitle:        t(trayKeys["UpTitle"]),
		UpBodyFmt:      t(trayKeys["UpBodyFmt"]),

		AboutTitle:   t(trayKeys["AboutTitle"]),
		AboutTagline: t(trayKeys["AboutTagline"]),
		AboutConsole: t(trayKeys["AboutConsole"]),
		AboutStatus:  t(trayKeys["AboutStatus"]),
		AboutData:    t(trayKeys["AboutData"]),
		AboutLicense: t(trayKeys["AboutLicense"]),
	}
}

// langOrder is the offer order shown to a user: English first because it is the
// fallback, then by the catalogue's own ordering.
func langOrder() []string { return cat.Langs }

func langName(tag string) string {
	if n := cat.Names[tag]; n != "" {
		return n
	}
	return tag
}

// bundle is what the console fetches: one language, flattened.
func bundle(lang string) map[string]any {
	s := map[string]string{}
	keys := make([]string, 0, len(cat.Strings))
	for k := range cat.Strings {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		s[k] = tr(lang, k)
	}
	return map[string]any{"lang": lang, "langs": cat.Langs, "names": cat.Names, "s": s}
}
