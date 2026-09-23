//go:build windows

package main

import "golang.org/x/sys/windows/registry"

// Language selection: an explicit choice wins, otherwise follow Windows.
//
// Following the OS is the right default — nobody wants to configure the language
// of a tray icon — but an override matters on servers, which are routinely
// installed in a language nobody on the team reads. The choice lives in HKCU, so
// each signed-in user gets their own and the unelevated tray can write it.

var procGetUserDefaultUILanguage = kernel32.NewProc("GetUserDefaultUILanguage")

// PRIMARYLANGID values for the languages the catalogue covers. Anything else
// falls back to English.
var winPrimaryLang = map[uint16]string{
	0x09: "en", 0x04: "zh", 0x0a: "es", 0x0c: "fr", 0x16: "pt",
	0x19: "ru", 0x21: "id", 0x07: "de", 0x11: "ja", 0x12: "ko",
}

// systemLang maps the Windows UI language to one of ours. Regional variants all
// collapse onto their base language: pt-BR and pt-PT share a catalogue entry, and
// every Chinese sublanguage lands on Simplified. Splitting those is adding an
// entry to strings.json and a case here, nothing more.
func systemLang() string {
	id, _, _ := procGetUserDefaultUILanguage.Call()
	if l, ok := winPrimaryLang[uint16(id)&0x3ff]; ok {
		return l
	}
	return langEN
}

// preferredLang is the tag actually in force.
func preferredLang() string {
	if v := storedLang(); v != langAuto {
		return v
	}
	return systemLang()
}

func storedLang() string {
	k, err := registry.OpenKey(registry.CURRENT_USER, `SOFTWARE\pingping`, registry.QUERY_VALUE)
	if err != nil {
		return langAuto
	}
	defer k.Close()
	v, _, err := k.GetStringValue("Language")
	if err != nil {
		return langAuto
	}
	if knownLang(v) {
		return v
	}
	return langAuto
}

func storeLang(tag string) {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, `SOFTWARE\pingping`, registry.SET_VALUE)
	if err != nil {
		return
	}
	defer k.Close()
	k.SetStringValue("Language", tag)
}
