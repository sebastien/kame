//go:build linux || darwin

package ownership

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"golang.org/x/sys/unix"
	"kame/internal/c"
	"os"
	"strings"
	"sync/atomic"
)

type nativeStore struct {
	root, kame, records, lock int
}
type nativeFile struct {
	fd     int
	stamp  unix.Stat_t
	name   string
	link   []byte
	offset int
}

func km_own_supported() bool { return true }
func ownParent(root int, name string, forbidden int) (int, string, c.Int) {
	if name == "" || strings.HasPrefix(name, "/") || strings.IndexByte(name, 0) >= 0 {
		return -1, "", -3
	}
	parts := strings.Split(name, "/")
	for _, p := range parts {
		if p == "" || p == "." || p == ".." {
			return -1, "", -3
		}
	}
	fd, e := unix.Dup(root)
	if e != nil {
		return -1, "", -1
	}
	for _, part := range parts[:len(parts)-1] {
		next, e := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(fd)
		if e != nil {
			if e == unix.ENOENT {
				return -1, "", 1
			}
			return -1, "", -3
		}
		fd = next
		if forbidden >= 0 {
			var dir, reserved unix.Stat_t
			if unix.Fstat(fd, &dir) != nil || unix.Fstat(forbidden, &reserved) != nil || (dir.Dev == reserved.Dev && dir.Ino == reserved.Ino) {
				unix.Close(fd)
				return -1, "", -3
			}
		}
	}
	return fd, parts[len(parts)-1], 0
}
func ownDir(parent int, name string, create bool) (int, c.Int) {
	if create {
		if e := unix.Mkdirat(parent, name, 0700); e != nil && e != unix.EEXIST {
			return -1, -1
		}
	}
	fd, e := unix.Openat(parent, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e == unix.ENOENT {
		return -1, 1
	}
	if e != nil {
		return -1, -3
	}
	return fd, 0
}
func km_own_open(root string, create bool, status *c.Int) *nativeStore {
	*status = -3
	if !strings.HasPrefix(root, "/") {
		return nil
	}
	slash, e := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if e != nil {
		*status = -1
		return nil
	}
	defer unix.Close(slash)
	parent, base, code := ownParent(slash, strings.TrimPrefix(root, "/"), -1)
	if code != 0 {
		*status = code
		return nil
	}
	defer unix.Close(parent)
	project, e := unix.Openat(parent, base, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil
	}
	s := &nativeStore{root: project, kame: -1, records: -1, lock: -1}
	success := false
	defer func() {
		if !success {
			km_own_close(s)
		}
	}()
	s.kame, *status = ownDir(project, ".kame", create)
	if *status != 0 {
		return nil
	}
	s.records, *status = ownDir(s.kame, "outputs", create)
	if *status != 0 {
		return nil
	}
	flags := unix.O_RDWR | unix.O_NOFOLLOW | unix.O_CLOEXEC
	if create {
		flags |= unix.O_CREAT
	}
	s.lock, e = unix.Openat(s.records, "lock", flags, 0600)
	var info unix.Stat_t
	if e != nil || unix.Fstat(s.lock, &info) != nil || info.Mode&unix.S_IFMT != unix.S_IFREG || info.Nlink != 1 {
		*status = -3
		return nil
	}
	*status = 0
	success = true
	return s
}
func km_own_lock_fd(s *nativeStore) c.Int { return c.Int(s.lock) }
func km_own_resource_fd(s *nativeStore, name string, status *c.Int) c.Int {
	*status = -3
	if s == nil || !safeKey(name) {
		return -1
	}
	dir, code := ownDir(s.records, "locks", true)
	if code != 0 {
		*status = code
		return -1
	}
	defer unix.Close(dir)
	fd, e := unix.Openat(dir, name, unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if e != nil {
		return -1
	}
	*status = 0
	return c.Int(fd)
}
func km_own_fd_close(fd c.Int) {
	if fd >= 0 {
		unix.Close(int(fd))
	}
}
func km_own_close(s *nativeStore) {
	if s != nil {
		for _, fd := range []int{s.lock, s.records, s.kame, s.root} {
			if fd >= 0 {
				unix.Close(fd)
			}
		}
		s.lock, s.records, s.kame, s.root = -1, -1, -1, -1
	}
}
func ownStamp(info *unix.Stat_t) [48]byte {
	var b [48]byte
	fields := []uint64{uint64(info.Dev), info.Ino, uint64(info.Mode), uint64(info.Size), uint64(info.Mtim.Sec*1e9 + info.Mtim.Nsec), uint64(info.Ctim.Sec*1e9 + info.Ctim.Nsec)}
	for i, v := range fields {
		binary.LittleEndian.PutUint64(b[i*8:], v)
	}
	return b
}
func km_own_root(s *nativeStore, data []byte) c.Int {
	if s == nil || len(data) != 16 {
		return -1
	}
	var info unix.Stat_t
	if unix.Fstat(s.root, &info) != nil {
		return -1
	}
	binary.LittleEndian.PutUint64(data, uint64(info.Dev))
	binary.LittleEndian.PutUint64(data[8:], info.Ino)
	return 0
}
func km_own_read(s *nativeStore, name string, artifact, links bool, status *c.Int) *nativeFile {
	*status = -3
	if s == nil {
		return nil
	}
	root, forbidden := s.records, -1
	if artifact {
		root, forbidden = s.root, s.kame
	}
	parent, base, code := ownParent(root, name, forbidden)
	if code != 0 {
		*status = code
		return nil
	}
	var info unix.Stat_t
	if links && unix.Fstatat(parent, base, &info, unix.AT_SYMLINK_NOFOLLOW) == nil && info.Mode&unix.S_IFMT == unix.S_IFLNK {
		for capacity := 256; capacity <= 16*1024*1024; capacity *= 2 {
			data := make([]byte, capacity)
			n, e := unix.Readlinkat(parent, base, data)
			if e != nil {
				break
			}
			if n < capacity {
				*status = 0
				return &nativeFile{fd: parent, stamp: info, name: base, link: data[:n]}
			}
		}
		unix.Close(parent)
		*status = -1
		return nil
	}
	fd, e := unix.Openat(parent, base, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	unix.Close(parent)
	if e != nil {
		if e == unix.ENOENT {
			*status = 1
		}
		return nil
	}
	if unix.Fstat(fd, &info) != nil || info.Mode&unix.S_IFMT != unix.S_IFREG {
		unix.Close(fd)
		return nil
	}
	*status = 0
	return &nativeFile{fd: fd, stamp: info}
}
func km_own_size(f *nativeFile) int64 { return f.stamp.Size }
func km_own_stamp(f *nativeFile, data []byte) c.Int {
	if f == nil || len(data) != 48 {
		return -1
	}
	stamp := ownStamp(&f.stamp)
	copy(data, stamp[:])
	return 0
}
func km_own_bytes(f *nativeFile, data []byte) c.Int {
	if f.name != "" {
		n := copy(data, f.link[f.offset:])
		f.offset += n
		return c.Int(n)
	}
	for {
		n, e := unix.Read(f.fd, data)
		if e == unix.EINTR {
			continue
		}
		if e != nil {
			return -1
		}
		return c.Int(n)
	}
}
func km_own_stable(f *nativeFile) bool {
	var now unix.Stat_t
	var e error
	if f.name != "" {
		e = unix.Fstatat(f.fd, f.name, &now, unix.AT_SYMLINK_NOFOLLOW)
	} else {
		e = unix.Fstat(f.fd, &now)
	}
	return e == nil && ownStamp(&now) == ownStamp(&f.stamp)
}
func km_own_file_close(f *nativeFile) {
	if f != nil && f.fd >= 0 {
		unix.Close(f.fd)
		f.fd = -1
	}
}
func ownRecordName(name string) bool {
	return name != "" && !strings.ContainsAny(name, "/\x00") && name != "." && name != ".."
}
func km_own_write(s *nativeStore, name string, data []byte) c.Int {
	if s == nil || !ownRecordName(name) {
		return -3
	}
	tmp := fmt.Sprintf(".tmp-%d-%d", os.Getpid(), ownWriteSerial.Add(1))
	fd, e := unix.Openat(s.records, tmp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if e != nil {
		return -1
	}
	success := false
	defer func() {
		if !success {
			unix.Unlinkat(s.records, tmp, 0)
		}
	}()
	remaining := data
	for len(remaining) > 0 {
		n, e := unix.Write(fd, remaining)
		if e == unix.EINTR {
			continue
		}
		if e != nil || n <= 0 {
			unix.Close(fd)
			return -1
		}
		remaining = remaining[n:]
	}
	syncErr := unix.Fsync(fd)
	closeErr := unix.Close(fd)
	if syncErr != nil || closeErr != nil || unix.Renameat(s.records, tmp, s.records, name) != nil || unix.Fsync(s.records) != nil {
		return -1
	}
	success = true
	return 0
}
func km_own_remove(s *nativeStore, name string) c.Int {
	if s == nil || !ownRecordName(name) {
		return -3
	}
	var info unix.Stat_t
	e := unix.Fstatat(s.records, name, &info, unix.AT_SYMLINK_NOFOLLOW)
	if e == unix.ENOENT {
		return 0
	}
	if e != nil || (info.Mode&unix.S_IFMT != unix.S_IFREG && !(strings.HasSuffix(name, ".artifact") && info.Mode&unix.S_IFMT == unix.S_IFLNK)) {
		return -3
	}
	if ownLedgerRemove(s.records, name) != nil || unix.Fsync(s.records) != nil {
		return -1
	}
	return 0
}
func km_own_list(s *nativeStore, data []byte) c.Int {
	fd, e := unix.Openat(s.records, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if e != nil {
		return -1
	}
	f := os.NewFile(uintptr(fd), "ownership records")
	defer f.Close()
	names, e := f.Readdirnames(-1)
	if e != nil {
		return -1
	}
	used := 0
	for _, name := range names {
		if !strings.HasSuffix(name, ".record") {
			continue
		}
		if used+len(name)+1 <= len(data) {
			copy(data[used:], name)
			data[used+len(name)] = 0
		}
		used += len(name) + 1
		if used > 16*1024*1024 {
			return -1
		}
	}
	return c.Int(used)
}
func km_own_capture(s *nativeStore, name, token string, expected []byte) c.Int {
	if s == nil || !ownRecordName(token) || len(expected) != 48 {
		return -3
	}
	parent, base, code := ownParent(s.root, name, s.kame)
	if code != 0 {
		return code
	}
	defer unix.Close(parent)
	var info, occupied unix.Stat_t
	e := unix.Fstatat(parent, base, &info, unix.AT_SYMLINK_NOFOLLOW)
	if e == unix.ENOENT {
		return 1
	}
	if e != nil {
		return -1
	}
	stamp := ownStamp(&info)
	if (info.Mode&unix.S_IFMT != unix.S_IFREG && info.Mode&unix.S_IFMT != unix.S_IFLNK) || !bytes.Equal(stamp[:], expected) {
		return 2
	}
	if unix.Fstatat(s.records, token, &occupied, unix.AT_SYMLINK_NOFOLLOW) != unix.ENOENT {
		return -3
	}
	if ownCaptureRename(parent, base, s.records, token) != nil || unix.Fsync(parent) != nil || unix.Fsync(s.records) != nil {
		return -1
	}
	return 0
}
func km_own_restore(s *nativeStore, name, token string) c.Int {
	if s == nil || !ownRecordName(token) {
		return -3
	}
	parent, base, code := ownParent(s.root, name, s.kame)
	if code != 0 {
		return -1
	}
	defer unix.Close(parent)
	if unix.Linkat(s.records, token, parent, base, 0) != nil || unix.Fsync(parent) != nil || unix.Unlinkat(s.records, token, 0) != nil || unix.Fsync(s.records) != nil {
		return -1
	}
	return 0
}

var ownWriteSerial atomic.Uint64
