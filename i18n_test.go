package main

import (
	"reflect"
	"regexp"
	"testing"
)

// A translation is either complete or it is a UI with blank menu items in it.
// Adding a field to msgs must break this test until every language has it, which
// is the whole reason the catalogue is a struct rather than a map.
func TestTranslationsAreComplete(t *testing.T) {
	langs := map[string]msgs{langEN: msgEN, langJA: msgJA, langZH: msgZH}
	typ := reflect.TypeOf(msgEN)

	for tag, m := range langs {
		v := reflect.ValueOf(m)
		for i := 0; i < typ.NumField(); i++ {
			if v.Field(i).String() == "" {
				t.Errorf("%s: %s is empty", tag, typ.Field(i).Name)
			}
		}
	}
	// Every language must be reachable through the selector.
	for _, tag := range langOrder {
		if langNames[tag] == "" {
			t.Errorf("%s has no display name, so the language menu cannot offer it", tag)
		}
	}
}

// A format string with the wrong number of verbs renders as %!d(MISSING) in a
// notification, in a language the author very likely cannot read. Compare each
// translation against English, which is the reference.
func TestFormatVerbsMatchEnglish(t *testing.T) {
	verb := regexp.MustCompile(`%[a-zA-Z]`)
	typ := reflect.TypeOf(msgEN)
	ref := reflect.ValueOf(msgEN)

	for tag, m := range map[string]msgs{langJA: msgJA, langZH: msgZH} {
		v := reflect.ValueOf(m)
		for i := 0; i < typ.NumField(); i++ {
			want := verb.FindAllString(ref.Field(i).String(), -1)
			got := verb.FindAllString(v.Field(i).String(), -1)
			if len(want) != len(got) {
				t.Errorf("%s: %s has %d format verbs, English has %d (%q vs %q)",
					tag, typ.Field(i).Name, len(got), len(want),
					v.Field(i).String(), ref.Field(i).String())
				continue
			}
			for j := range want {
				if want[j] != got[j] {
					t.Errorf("%s: %s verb %d is %s, English has %s",
						tag, typ.Field(i).Name, j+1, got[j], want[j])
				}
			}
		}
	}
}
