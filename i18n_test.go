package main

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// A translation is either complete or it is a UI with blank menu items in it.
// Adding a key to strings.json must break this until every language has it.
func TestCatalogueIsComplete(t *testing.T) {
	if len(cat.Langs) == 0 || len(cat.Strings) == 0 {
		t.Fatal("the catalogue did not load")
	}
	for key, byLang := range cat.Strings {
		for _, l := range cat.Langs {
			if strings.TrimSpace(byLang[l]) == "" {
				t.Errorf("%s is missing or empty for %q", key, l)
			}
		}
		for l := range byLang {
			if !knownLang(l) {
				t.Errorf("%s carries %q, which is not in the language list", key, l)
			}
		}
	}
	for _, l := range cat.Langs {
		if strings.TrimSpace(cat.Names[l]) == "" {
			t.Errorf("%q has no display name, so no picker can offer it", l)
		}
	}
	t.Logf("%d keys × %d languages = %d strings",
		len(cat.Strings), len(cat.Langs), len(cat.Strings)*len(cat.Langs))
}

// A format string with the wrong verbs renders as %!d(MISSING) inside a
// notification, in a language the author very likely cannot read. English is the
// reference; every translation must agree with it.
func TestFormatVerbsMatchEnglish(t *testing.T) {
	verb := regexp.MustCompile(`%[a-zA-Z]`)
	for key, byLang := range cat.Strings {
		want := verb.FindAllString(byLang[langEN], -1)
		for _, l := range cat.Langs {
			if l == langEN {
				continue
			}
			got := verb.FindAllString(byLang[l], -1)
			if len(got) != len(want) {
				t.Errorf("%s [%s]: %d format verbs, English has %d\n  %s\n  %s",
					key, l, len(got), len(want), byLang[l], byLang[langEN])
				continue
			}
			for i := range want {
				if want[i] != got[i] {
					t.Errorf("%s [%s]: verb %d is %s, English has %s",
						key, l, i+1, got[i], want[i])
				}
			}
		}
	}
}

// Every field the notification-area icon reads must map to a key that exists.
// A renamed key would otherwise surface as the key itself in somebody's menu.
func TestTrayKeysResolve(t *testing.T) {
	typ := reflect.TypeOf(msgs{})
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		key, ok := trayKeys[name]
		if !ok {
			t.Errorf("msgs.%s has no catalogue key", name)
			continue
		}
		if _, ok := cat.Strings[key]; !ok {
			t.Errorf("msgs.%s points at %q, which is not in strings.json", name, key)
		}
	}
	// And the projection must come out fully populated in every language.
	for _, l := range cat.Langs {
		m := reflect.ValueOf(messages(l))
		for i := 0; i < typ.NumField(); i++ {
			if m.Field(i).String() == "" {
				t.Errorf("messages(%q).%s is empty", l, typ.Field(i).Name)
			}
		}
	}
}

// Tags arrive from browsers and from Windows in shapes the catalogue does not use
// verbatim. Mapping them is the difference between a Brazilian user getting
// Portuguese and getting English.
func TestNormalizeLang(t *testing.T) {
	cases := map[string]string{
		"en": "en", "EN": "en", "en-US": "en", "en_GB": "en",
		"zh": "zh", "zh-Hans-CN": "zh", "zh-TW": "zh",
		"pt-BR": "pt", "pt_PT": "pt", "ja-JP": "ja", "ko-KR": "ko",
		"de-AT": "de", "id-ID": "id", "ru-RU": "ru", "fr-CA": "fr", "es-MX": "es",
		"": "", "xx": "", "klingon": "",
	}
	for in, want := range cases {
		if got := normalizeLang(in); got != want {
			t.Errorf("normalizeLang(%q) = %q, want %q", in, got, want)
		}
	}
}

// A browser that says "ja, en;q=0.8" means it.
func TestAcceptLanguage(t *testing.T) {
	cases := map[string]string{
		"ja,en;q=0.8":             "ja",
		"en-US,en;q=0.9":          "en",
		"zh-CN,zh;q=0.9,en;q=0.8": "zh",
		"pt-BR,pt;q=0.9":          "pt",
		"xx-YY,ko;q=0.7":          "ko",
		"":                        "en",
		"xx":                      "en",
	}
	for in, want := range cases {
		if got := acceptLanguage(in); got != want {
			t.Errorf("acceptLanguage(%q) = %q, want %q", in, got, want)
		}
	}
}

// Anything the pages ask for by data-i18n must exist, or it renders as a raw key.
func TestPageKeysExist(t *testing.T) {
	attr := regexp.MustCompile(`data-i18n(?:-ph|-title)?="([^"]+)"`)
	pages, err := staticFS.ReadDir("static")
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, p := range pages {
		if !strings.HasSuffix(p.Name(), ".html") {
			continue
		}
		b, _ := staticFS.ReadFile("static/" + p.Name())
		for _, m := range attr.FindAllStringSubmatch(string(b), -1) {
			seen++
			if _, ok := cat.Strings[m[1]]; !ok {
				t.Errorf("%s asks for %q, which is not in strings.json", p.Name(), m[1])
			}
		}
	}
	if seen == 0 {
		t.Fatal("no data-i18n attributes found — the matcher has stopped working")
	}
	t.Logf("checked %d translated elements across the console pages", seen)
}
