//go:build windows

package securefs

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsPrivacyRejectsAccessForEveryone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "record")
	if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	everyone, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		t.Fatal(err)
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{
		allowEntry(everyone, windows.TRUSTEE_IS_WELL_KNOWN_GROUP, windows.NO_INHERITANCE),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		acl,
		nil,
	); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	private, err := IsPrivateRegularFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if private {
		t.Fatal("file readable by Everyone was accepted as private")
	}
}
