package hostagent

import "testing"

func TestLicenseCmdValidation(t *testing.T) {
	bad := "not a key"
	if err := validateCommand(TypeLicense, &LicenseCmd{Key: &bad}); err == nil {
		t.Fatal("a non-key was accepted")
	}
	ok := "ATL1.abc.def"
	day := "2026-10-11"
	if err := validateCommand(TypeLicense, &LicenseCmd{Key: &ok, State: &LicenseState{Status: "replaced", StopsOn: &day}}); err != nil {
		t.Fatal(err)
	}
	if err := validateCommand(TypeLicense, &LicenseCmd{State: &LicenseState{Status: "weird"}}); err == nil {
		t.Fatal("a bad status was accepted")
	}
	notDay := "tomorrow"
	if err := validateCommand(TypeLicense, &LicenseCmd{State: &LicenseState{Status: "past_due", StopsOn: &notDay}}); err == nil {
		t.Fatal("a bad date was accepted")
	}
	if newCommand(TypeLicense) == nil {
		t.Fatal("host.license is not a command")
	}
}
