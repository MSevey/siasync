package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	sia "gitlab.com/NebulousLabs/Sia/node/api/client"
	"gitlab.com/NebulousLabs/Sia/siatest"
)

const testDir = "test"

var (
	// siaSyncTestingDir is the directory that contains all of the files and
	// folders created during testing.
	siaSyncTestingDir = filepath.Join(os.TempDir(), "SiaSync")

	// These are the files that should be uploaded
	testFiles = []string{"test", "testdir/testfile3.txt", "testdir/testdir2/testfile4.txt", "testfile1.txt", "testfile2.txt"}
)

// mainTestDir creates a testing directory for tests in the main package.
func mainTestDir(testName string) string {
	path := filepath.Join(siaSyncTestingDir, filepath.Join("main", testName))
	err := os.RemoveAll(path)
	if err != nil {
		panic(err)
	}
	if err := os.MkdirAll(path, 0777); err != nil {
		panic(err)
	}
	return path
}

// newTestClient returns a client that SiaSync needs for testing
func newTestClient(tn *siatest.TestNode) *sia.Client {
	sc := sia.New(tn.Address)
	sc.Password = tn.Password
	sc.UserAgent = tn.UserAgent
	return sc
}

// TestSiafolder confirms that a call to NewSiaFolder will upload all the files
// in the test directory
func TestSiafolder(t *testing.T) {
	// Create a group
	groupParams := siatest.GroupParams{
		Hosts:   5,
		Renters: 1,
		Miners:  1,
	}
	tg, err := siatest.NewGroupFromTemplate(mainTestDir(t.Name()), groupParams)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := tg.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Create a client for siasync
	sc := newTestClient(tg.Renters()[0])

	// Create a new siafolder
	sf, err := NewSiafolder(testDir, sc)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := sf.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Confirm all the files were uploaded
	for _, file := range testFiles {
		if _, exists := sf.files[file]; !exists {
			t.Fatalf("File %v not uploaded to siafolder", file)
		}
	}
}

// TestSiafolderCreateDelete verifies that files created or removed in the
// watched directory are correctly uploaded and deleted.
func TestSiafolderCreateDelete(t *testing.T) {
	// Create a group
	groupParams := siatest.GroupParams{
		Hosts:   5,
		Renters: 1,
		Miners:  1,
	}
	tg, err := siatest.NewGroupFromTemplate(mainTestDir(t.Name()), groupParams)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := tg.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Create a client for siasync
	sc := newTestClient(tg.Renters()[0])

	// Create a new siafolder
	sf, err := NewSiafolder(testDir, sc)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := sf.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// create a new file and write some data to it and verify that it gets
	// uploaded
	newfile := filepath.Join(testDir, "newfile")
	f, err := os.Create(newfile)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		f.Close()
		os.Remove(f.Name())
	}()

	// wait a bit for the filesystem event to propagate
	time.Sleep(time.Second)

	if _, exists := sf.files["newfile"]; !exists {
		t.Fatal("newfile should have been uploaded when it was created on disk")
	}

	// delete the file
	f.Close()
	os.Remove(f.Name())

	time.Sleep(time.Second)

	if _, exists := sf.files["newfile"]; exists {
		t.Fatal("newfile should have been deleted when it was removed on disk")
	}

	// test that events propagate in nested directories
	newfile = filepath.Join(testDir, "testdir/newfile")
	f, err = os.Create(newfile)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		f.Close()
		os.Remove(f.Name())
	}()

	time.Sleep(time.Second)

	if _, exists := sf.files["testdir/newfile"]; !exists {
		t.Fatal("newfile should have been uploaded when it was created on disk")
	}

	// delete the file
	f.Close()
	os.Remove(f.Name())

	time.Sleep(time.Second)

	if _, exists := sf.files["testdir/newfile"]; exists {
		t.Fatal("newfile should have been deleted when it was removed on disk")
	}
}

// TestSiafolderCreateDirectory verifies that files in newly created
// directories under the watched directory get correctly uploaded.
func TestSiafolderCreateDirectory(t *testing.T) {
	// Create a group
	groupParams := siatest.GroupParams{
		Hosts:   5,
		Renters: 1,
		Miners:  1,
	}
	tg, err := siatest.NewGroupFromTemplate(mainTestDir(t.Name()), groupParams)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := tg.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Create a client for siasync
	sc := newTestClient(tg.Renters()[0])

	// Create a new siafolder
	sf, err := NewSiafolder(testDir, sc)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := sf.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	testCreateDir := filepath.Join(testDir, "newdir")
	err = os.Mkdir(testCreateDir, 0755)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(testCreateDir)

	// should not upload empty directories
	time.Sleep(time.Second)

	if _, exists := sf.files["newdir"]; exists {
		t.Fatal("should not upload empty directories")
	}

	testFile := filepath.Join(testCreateDir, "testfile")
	f, err := os.Create(testFile)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	time.Sleep(time.Second)

	if _, exists := sf.files["newdir/testfile"]; !exists {
		t.Fatal("should have uploaded file in newly created directory")
	}
}

// TestSiafolderFileWrite verifies that a file is deleted and re-uploaded when
// it is changed on disk.
func TestSiafolderFileWrite(t *testing.T) {
	// Create a group
	groupParams := siatest.GroupParams{
		Hosts:   5,
		Renters: 1,
		Miners:  1,
	}
	tg, err := siatest.NewGroupFromTemplate(mainTestDir(t.Name()), groupParams)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := tg.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Create a client for siasync
	sc := newTestClient(tg.Renters()[0])

	// Create a new siafolder
	sf, err := NewSiafolder(testDir, sc)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := sf.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	newfile := filepath.Join(testDir, "newfile")
	f, err := os.Create(newfile)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		f.Close()
		os.Remove(f.Name())
	}()

	// wait a bit for the filesystem event to propagate
	time.Sleep(time.Second)

	oldChecksum, exists := sf.files["newfile"]
	if !exists {
		t.Fatal("newfile should have been uploaded when it was created on disk")
	}

	// write some data to the file and verify that it is updated
	_, err = f.Write([]byte{40, 40, 40, 40, 40})
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(time.Second)

	newChecksum, exists := sf.files["newfile"]
	if !exists {
		t.Fatal("newfile did not exist after writing data to it")
	}
	if newChecksum == oldChecksum {
		t.Fatal("checksum did not change")
	}
}
