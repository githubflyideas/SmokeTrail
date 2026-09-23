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

const (
	langPrimaryEnglish  = 0x09
	langPrimaryJapanese = 0x11
	langPrimaryChinese  = 0x04
)

// systemLang maps the Windows UI language to one of ours.
func systemLang() string {
	id, _, _ := procGetUserDefaultUILanguage.Call()
	switch uint16(id) & 0x3ff { // PRIMARYLANGID
	case langPrimaryJapanese:
		return langJA
	case langPrimaryChinese:
		// Every Chinese sublanguage lands on Simplified for now. Traditional
		// would be a fourth msgs value and a sublanguage check here.
		return langZH
	case langPrimaryEnglish:
		return langEN
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
	k, err := registry.OpenKey(registry.CURRENT_USER, `SOFTWARE\SmokeTrail`, registry.QUERY_VALUE)
	if err != nil {
		return langAuto
	}
	defer k.Close()
	v, _, err := k.GetStringValue("Language")
	if err != nil {
		return langAuto
	}
	switch v {
	case langEN, langJA, langZH:
		return v
	}
	return langAuto
}

func storeLang(tag string) {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, `SOFTWARE\SmokeTrail`, registry.SET_VALUE)
	if err != nil {
		return
	}
	defer k.Close()
	k.SetStringValue("Language", tag)
}
