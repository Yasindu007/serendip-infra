package main

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// makePrivate gives dir an access list naming only the current user, with
// nothing inherited from its parents. On Windows a folder's permissions
// come from its parents, and even %AppData% can grant other accounts read
// access (spec 021 T011 found a sandbox group there), so the sign-in file
// is private only if its folder is. Files created in dir afterwards
// inherit the same list.
func makePrivate(dir string) error {
	tok := windows.GetCurrentProcessToken()
	user, err := tok.GetTokenUser()
	if err != nil {
		return fmt.Errorf("find the current user: %w", err)
	}
	sid := user.User.Sid.String()
	// P: protected, so nothing is inherited; OICI: files and folders
	// inside inherit it; FA: full access, for this user alone.
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + sid + ")")
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	if err := windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		return fmt.Errorf("restrict %s to its owner: %w", dir, err)
	}
	return nil
}
