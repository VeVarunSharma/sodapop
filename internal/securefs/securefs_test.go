package securefs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrivateDirectoryAndFile(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "private")
	if err := MkdirAllPrivate(directory); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "record")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0666)
	if err != nil {
		t.Fatal(err)
	}
	if err := ProtectFile(file); err != nil {
		file.Close()
		t.Fatal(err)
	}
	private, err := IsPrivateRegularFile(file)
	closeErr := file.Close()
	if err != nil || closeErr != nil || !private {
		t.Fatalf("private file validation = %t, %v, close=%v", private, err, closeErr)
	}

	renamed := filepath.Join(directory, "renamed")
	if err := os.Rename(path, renamed); err != nil {
		t.Fatal(err)
	}
	file, err = os.Open(renamed)
	if err != nil {
		t.Fatal(err)
	}
	private, err = IsPrivateRegularFile(file)
	closeErr = file.Close()
	if err != nil || closeErr != nil || !private {
		t.Fatalf("renamed private file validation = %t, %v, close=%v", private, err, closeErr)
	}
}
