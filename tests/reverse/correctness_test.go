package reverse_test

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/rfjakob/gocryptfs/internal/ctlsock"
	"github.com/rfjakob/gocryptfs/internal/syscallcompat"
	"github.com/rfjakob/gocryptfs/tests/test_helpers"
)

// TestLongnameStat checks that file names of all sizes (1 to 255) show up in
// the decrypted reverse view (dirC, mounted in TestMain).
func TestLongnameStat(t *testing.T) {
	for i := 1; i <= 255; i++ {
		name := string(bytes.Repeat([]byte("x"), i))
		fd, err := os.Create(dirA + "/" + name)
		if err != nil {
			t.Fatal(err)
		}
		fd.Close()
		path := dirC + "/" + name
		if !test_helpers.VerifyExistence(path) {
			t.Fatalf("failed to verify %q", path)
		}
		test_helpers.VerifySize(t, path, 0)
		// A large number of longname files is a performance problem in
		// reverse mode. Move the file out of the way once we are done with it
		// to speed up the test (2 seconds -> 0.2 seconds).
		// We do NOT unlink it because ext4 reuses inode numbers immediately,
		// which will cause "Found linked inode, but Nlink == 1" warnings and
		// file not found errors.
		// TODO: This problem should be handled at the go-fuse level.
		syscall.Rename(dirA+"/"+name, test_helpers.TmpDir+"/"+fmt.Sprintf("x%d", i))
	}
}

func TestSymlinks(t *testing.T) {
	target := "/"
	os.Symlink(target, dirA+"/symlink")
	cSymlink := dirC + "/symlink"
	_, err := os.Lstat(cSymlink)
	if err != nil {
		t.Errorf("Lstat: %v", err)
	}
	_, err = os.Stat(cSymlink)
	if err != nil {
		t.Errorf("Stat: %v", err)
	}
	actualTarget, err := os.Readlink(cSymlink)
	if err != nil {
		t.Fatal(err)
	}
	if target != actualTarget {
		t.Errorf("wrong symlink target: want=%q have=%q", target, actualTarget)
	}
}

// Symbolic link dentry sizes should be set to the length of the string
// that contains the target path.
func TestSymlinkDentrySize(t *testing.T) {
	if plaintextnames {
		t.Skip("this only tests encrypted names")
	}
	symlink := "a_symlink"

	mnt, err := ioutil.TempDir(test_helpers.TmpDir, "reverse_mnt_")
	if err != nil {
		t.Fatal(err)
	}

	sock := mnt + ".sock"
	test_helpers.MountOrFatal(t, "ctlsock_reverse_test_fs", mnt, "-reverse", "-extpass", "echo test", "-ctlsock="+sock)
	defer test_helpers.UnmountPanic(mnt)

	req := ctlsock.RequestStruct{EncryptPath: symlink}
	symlinkResponse := test_helpers.QueryCtlSock(t, sock, req)
	if symlinkResponse.ErrNo != 0 {
		t.Errorf("Encrypt: %q ErrNo=%d ErrText=%s", symlink, symlinkResponse.ErrNo, symlinkResponse.ErrText)
	}

	fi, err := os.Lstat(mnt + "/" + symlinkResponse.Result)
	if err != nil {
		t.Errorf("Lstat: %v", err)
	}

	target, err := os.Readlink(mnt + "/" + symlinkResponse.Result)
	if err != nil {
		t.Errorf("Readlink: %v", err)
	}

	if fi.Size() != int64(len(target)) {
		t.Errorf("Lstat reports that symbolic link %q's dentry size is %d, but this does not "+
			"match the length of the string returned by readlink, which is %d.",
			symlink, fi.Size(), len(target))
	}
}

// .gocryptfs.reverse.conf in the plaintext dir should be visible as
// gocryptfs.conf
func TestConfigMapping(t *testing.T) {
	c := dirB + "/gocryptfs.conf"
	if !test_helpers.VerifyExistence(c) {
		t.Errorf("%s missing", c)
	}
	data, err := ioutil.ReadFile(c)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Errorf("empty file")
	}
}

// Check that the access() syscall works on virtual files
func TestAccessVirtual(t *testing.T) {
	if plaintextnames {
		t.Skip("test makes no sense for plaintextnames")
	}
	var R_OK uint32 = 4
	var W_OK uint32 = 2
	var X_OK uint32 = 1
	fn := dirB + "/gocryptfs.diriv"
	err := syscall.Access(fn, R_OK)
	if err != nil {
		t.Errorf("%q should be readable, but got error: %v", fn, err)
	}
	err = syscall.Access(fn, W_OK)
	if err == nil {
		t.Errorf("should NOT be writeable")
	}
	err = syscall.Access(fn, X_OK)
	if err == nil {
		t.Errorf("should NOT be executable")
	}
}

// Check that the access() syscall works on regular files
func TestAccess(t *testing.T) {
	f, err := os.Create(dirA + "/testaccess1")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	f, err = os.Open(dirB)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	names, err := f.Readdirnames(0)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		// Check if file exists - this should never fail
		err = syscallcompat.Faccessat(unix.AT_FDCWD, dirB+"/"+n, unix.F_OK)
		if err != nil {
			t.Errorf("%s: %v", n, err)
		}
		// Check if file is readable
		err = syscallcompat.Faccessat(unix.AT_FDCWD, dirB+"/"+n, unix.R_OK)
		if err != nil {
			t.Logf("%s: %v", n, err)
		}
	}
}

// Opening a nonexistent file name should return ENOENT
// and not EBADMSG or EIO or anything else.
func TestEnoent(t *testing.T) {
	fn := dirB + "/TestEnoent"
	_, err := syscall.Open(fn, syscall.O_RDONLY, 0)
	if err != syscall.ENOENT {
		t.Errorf("want ENOENT, got: %v", err)
	}
}

// If the symlink target gets too long due to base64 encoding, we should
// return ENAMETOOLONG instead of having the kernel reject the data and
// returning an I/O error to the user.
// https://github.com/rfjakob/gocryptfs/issues/167
func TestTooLongSymlink(t *testing.T) {
	var err error
	var l int
	fn := dirA + "/TooLongSymlink"
	// Try 4000 first (works on ext4 and tmpfs), then retry with 1000 (XFS and
	// Darwin have a limit of about 1024)
	for _, l = range []int{4000, 1000} {
		target := string(bytes.Repeat([]byte("x"), l))
		err = os.Symlink(target, fn)
		if err == nil {
			break
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Created symlink of length %d", l)
	_, err = os.Readlink(dirC + "/TooLongSymlink")
	if err == nil {
		return
	}
	err2 := err.(*os.PathError)
	if err2.Err != syscall.ENAMETOOLONG {
		t.Errorf("Expected %q error, got %q instead", syscall.ENAMETOOLONG,
			err2.Err)
	}
}

// Test that we can traverse a directory with 0100 permissions
// (execute but no read). This used to be a problem as OpenDirNofollow opened
// all directories in the path with O_RDONLY. Now it uses O_PATH, which only needs
// the executable bit.
func Test0100Dir(t *testing.T) {
	// Note: t.Name() is not available before in Go 1.8
	tName := "Test0100Dir"
	dir := dirA + "/" + tName
	err := os.Mkdir(dir, 0700)
	if err != nil {
		t.Fatal(err)
	}
	file := dir + "/hello"
	err = ioutil.WriteFile(file, []byte("hello"), 0600)
	if err != nil {
		t.Fatal(err)
	}
	err = os.Chmod(dir, 0100)
	if err != nil {
		t.Fatal(err)
	}

	fileReverse := dirC + "/" + tName + "/hello"
	fd, err := os.Open(fileReverse)
	// Make sure the dir can be removed after the test is done
	os.Chmod(dir, 0700)
	if err != nil {
		t.Fatal(err)
	}
	fd.Close()
}
