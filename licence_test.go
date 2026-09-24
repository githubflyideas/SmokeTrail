package main

import (
	"strings"
	"testing"
)

// The binary tells people which licence it is under, in the About dialog and on
// the settings page. Both have to agree with the file in the repository, which
// is the only one of the three that has any legal effect — and this project
// once claimed MIT in four places, including the compiled version resource,
// over an Apache 2.0 LICENSE.
func TestLicenceClaimMatchesTheFile(t *testing.T) {
	file := readSource(t, "LICENSE")

	if !strings.Contains(file, "Apache License") || !strings.Contains(file, "Version 2.0") {
		t.Fatalf("LICENSE is not Apache 2.0 any more; licenceName says %q", licenceName)
	}
	if licenceName != "Apache License 2.0" {
		t.Errorf("licenceName is %q but LICENSE is Apache 2.0", licenceName)
	}
	if got := readSource(t, "static/settings.html"); !strings.Contains(got, licenceName) {
		t.Errorf("the settings page does not show %q", licenceName)
	}
	if got := readSource(t, "NOTICE"); !strings.Contains(got, "Apache") {
		t.Errorf("NOTICE does not mention the licence")
	}
}

// CreateService fills a zero ServiceType and StartType in for you; UpdateConfig
// hands the struct straight to ChangeServiceConfig, where zero is invalid rather
// than "unchanged". One config literal served both paths, so every fresh install
// worked and every upgrade failed with "the parameter is incorrect", which names
// no parameter.
//
// This is Windows-only code, so this reads it rather than compiling it — the
// same reason the tray menu tests do.
func TestServiceConfigIsCompletedBeforeUpdateConfig(t *testing.T) {
	src := readSource(t, "service_windows.go")

	update := strings.Index(src, "UpdateConfig(c)")
	if update < 0 {
		t.Skip("no UpdateConfig call any more")
	}
	for _, field := range []string{"c.ServiceType =", "c.StartType ="} {
		at := strings.Index(src, field)
		if at < 0 {
			t.Errorf("%s is never set, so UpdateConfig receives a zero for it", field)
			continue
		}
		if at > update {
			t.Errorf("%s is set after the UpdateConfig call, not before it", field)
		}
	}
}
