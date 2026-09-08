//go:build windows

package securefs

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

func protectPath(path string, directory bool) error {
	user, system, administrators, err := privatePrincipals()
	if err != nil {
		return err
	}
	inheritance := uint32(windows.NO_INHERITANCE)
	if directory {
		inheritance = windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT
	}
	entries := []windows.EXPLICIT_ACCESS{
		allowEntry(user, windows.TRUSTEE_IS_USER, inheritance),
		allowEntry(system, windows.TRUSTEE_IS_USER, inheritance),
		allowEntry(administrators, windows.TRUSTEE_IS_GROUP, inheritance),
	}
	acl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		acl,
		nil,
	)
}

func protectFile(file *os.File) error {
	return protectPath(file.Name(), false)
}

func isPrivateFile(file *os.File, _ os.FileInfo) (bool, error) {
	user, system, administrators, err := privatePrincipals()
	if err != nil {
		return false, err
	}
	var descriptor *windows.SECURITY_DESCRIPTOR
	connection, err := file.SyscallConn()
	if err != nil {
		return false, err
	}
	var callErr error
	if err := connection.Control(func(handle uintptr) {
		descriptor, callErr = windows.GetSecurityInfo(
			windows.Handle(handle),
			windows.SE_FILE_OBJECT,
			windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
		)
	}); err != nil {
		return false, err
	}
	if callErr != nil {
		return false, callErr
	}
	owner, _, err := descriptor.Owner()
	if err != nil {
		return false, err
	}
	if owner == nil || !owner.Equals(user) {
		return false, nil
	}
	control, _, err := descriptor.Control()
	if err != nil {
		return false, err
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		return false, nil
	}
	acl, _, err := descriptor.DACL()
	if err != nil || acl == nil {
		return false, err
	}
	hasUserAccess := false
	for index := uint16(0); index < acl.AceCount; index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, uint32(index), &ace); err != nil {
			return false, err
		}
		switch ace.Header.AceType {
		case windows.ACCESS_DENIED_ACE_TYPE:
			continue
		case windows.ACCESS_ALLOWED_ACE_TYPE:
		default:
			return false, nil
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if sid.Equals(user) {
			hasUserAccess = true
			continue
		}
		if !sid.Equals(system) && !sid.Equals(administrators) {
			return false, nil
		}
	}
	return hasUserAccess, nil
}

func privatePrincipals() (user, system, administrators *windows.SID, err error) {
	tokenUser, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, nil, nil, err
	}
	user, err = tokenUser.User.Sid.Copy()
	if err != nil {
		return nil, nil, nil, err
	}
	system, err = windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return nil, nil, nil, err
	}
	administrators, err = windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	return user, system, administrators, err
}

func allowEntry(sid *windows.SID, trusteeType windows.TRUSTEE_TYPE, inheritance uint32) windows.EXPLICIT_ACCESS {
	return windows.EXPLICIT_ACCESS{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.SET_ACCESS,
		Inheritance:       inheritance,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  trusteeType,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}
}
