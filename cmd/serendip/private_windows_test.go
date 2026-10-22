package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// The sign-in file is private to its owner whatever its folder's parents
// allow: the folder gets a protected access list naming only the user, and
// the file inherits it.
func TestCredentialsArePrivateOnWindows(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "serendip")
	if err := saveCredentials(dir, &credentials{Server: "http://localhost:8080", AccessToken: "x", Expiry: time.Now()}); err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	me := user.User.Sid.String()
	for _, path := range []string{dir, credentialsPath(dir)} {
		sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			t.Fatal(err)
		}
		s := sd.String()
		// The folder blocks inheritance from its parents; the file inherits
		// the folder's owner-only list.
		if path == dir && !strings.HasPrefix(s, "D:P") {
			t.Errorf("%s: access list %s isn't protected from inheritance", path, s)
		}
		aces := strings.Count(s, "(A;")
		if aces != strings.Count(s, ";;;"+me+")") || aces == 0 {
			t.Errorf("%s: access list %s grants someone other than %s", path, s, me)
		}
	}
}
