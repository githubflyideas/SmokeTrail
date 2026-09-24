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
