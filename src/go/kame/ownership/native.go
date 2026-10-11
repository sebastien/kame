// Package ownership implements conservative historical cleanup on native hosts.
package ownership

import (
	"kame/core"
	"solod.dev/so/c"
	"solod.dev/so/mem"
	"solod.dev/so/path"
	"solod.dev/so/slices"
)

//so:embed native.h
var native_h string

//so:embed native.c
var native_c string

//so:extern km_own_store
type nativeStore struct{}

//so:extern km_own_file
type nativeFile struct{}

//so:extern
func km_own_supported() bool { return false }

func Supported() bool { return km_own_supported() }

//so:extern nodecay
func km_own_open(root string, create bool, wait bool, status *c.Int) *nativeStore {
	_, _, _, _ = root, create, wait, status
	return nil
}

//so:extern
func km_own_close(s *nativeStore) { _ = s }

//so:extern nodecay
func km_own_root(s *nativeStore, data []byte) c.Int { _, _ = s, data; return -1 }

//so:extern nodecay
func km_own_read(s *nativeStore, name string, artifact bool, status *c.Int) *nativeFile {
	_, _, _, _ = s, name, artifact, status
	return nil
}

//so:extern
func km_own_size(f *nativeFile) int64 { _ = f; return 0 }

//so:extern nodecay
func km_own_stamp(f *nativeFile, data []byte) c.Int { _, _ = f, data; return -1 }

//so:extern nodecay
func km_own_bytes(f *nativeFile, data []byte) c.Int { _, _ = f, data; return -1 }

//so:extern
func km_own_stable(f *nativeFile) bool { _ = f; return false }

//so:extern
func km_own_file_close(f *nativeFile) { _ = f }

//so:extern nodecay
func km_own_write(s *nativeStore, name string, data []byte) c.Int { _, _, _ = s, name, data; return -1 }

//so:extern nodecay
func km_own_remove(s *nativeStore, name string) c.Int { _, _ = s, name; return -1 }

//so:extern nodecay
func km_own_list(s *nativeStore, data []byte) c.Int { _, _ = s, data; return -1 }

//so:extern nodecay
func km_own_capture(s *nativeStore, name string, token string, expected []byte) c.Int {
	_, _, _, _ = s, name, token, expected
	return -1
}

//so:extern nodecay
func km_own_restore(s *nativeStore, name string, token string) c.Int {
	_, _, _ = s, name, token
	return -1
}

type Store struct {
	Alloc       mem.Allocator
	Root        string
	native      *nativeStore
	ID          [16]byte
	Status      int
	PollCancel  func(any) bool
	CancelState any
	Interrupted bool
}

func (s *Store) cancelled() bool {
	if s.Interrupted {
		return true
	}
	if s.PollCancel != nil && s.PollCancel(s.CancelState) {
		s.Interrupted = true
	}
	return s.Interrupted
}

func New(a mem.Allocator, root string) *Store {
	s := mem.Alloc[Store](a)
	s.Alloc, s.Root = a, path.Clean(a, root)
	return s
}

func (s *Store) Open(create bool) bool {
	return s.open(create, false)
}

// Acquire serializes producers, while inspection/protection/cleanup stay
// fail-fast. Signal delivery interrupts the blocking advisory lock wait.
func (s *Store) Acquire() bool {
	return s.open(true, true)
}

func (s *Store) open(create bool, wait bool) bool {
	if s.native != nil {
		return true
	}
	var status c.Int
	s.native = km_own_open(s.Root, create, wait, &status)
	s.Status = int(status)
	if s.native != nil && km_own_root(s.native, s.ID[:]) != 0 {
		km_own_close(s.native)
		s.native = nil
		s.Status = -1
	}
	return s.native != nil
}

func (s *Store) Free() {
	if s == nil {
		return
	}
	km_own_close(s.native)
	mem.FreeString(s.Alloc, s.Root)
	mem.Free(s.Alloc, s)
}

func clone(a mem.Allocator, text string) string {
	b := slices.Make[byte](a, len(text))
	copy(b, text)
	return string(b)
}

func hash(data []byte) core.Signature {
	d := core.NewDigest()
	d.Write(data)
	result := core.Signature{Mode: core.SignatureContent}
	d.Sum(result.Digest[:])
	return result
}

func key(a mem.Allocator, text string) string {
	d := hash([]byte(text))
	b := slices.Make[byte](a, 64)
	const hex = "0123456789abcdef"
	for i := range d.Digest {
		b[i*2], b[i*2+1] = hex[d.Digest[i]>>4], hex[d.Digest[i]&15]
	}
	return string(b)
}

type observation struct {
	Stamp  [48]byte
	Digest [32]byte
	Status int
}

func (s *Store) observe(name string, artifact bool) observation {
	var status c.Int
	f := km_own_read(s.native, name, artifact, &status)
	result := observation{Status: int(status)}
	if f == nil {
		return result
	}
	km_own_stamp(f, result.Stamp[:])
	buffer := slices.Make[byte](s.Alloc, 32768)
	d := core.NewDigest()
	for {
		if s.cancelled() {
			result.Status = -4
			break
		}
		n := int(km_own_bytes(f, buffer))
		if n < 0 {
			result.Status = -1
			break
		}
		if n == 0 {
			break
		}
		d.Write(buffer[:n])
	}
	if result.Status == 0 && !km_own_stable(f) {
		result.Status = 2
	}
	d.Sum(result.Digest[:])
	slices.Free(s.Alloc, buffer)
	km_own_file_close(f)
	return result
}

func (s *Store) read(name string) []byte {
	var status c.Int
	f := km_own_read(s.native, name, false, &status)
	if f == nil {
		s.Status = int(status)
		return nil
	}
	size := km_own_size(f)
	if size < 0 || size > 16*1024*1024 {
		km_own_file_close(f)
		s.Status = -3
		return nil
	}
	data := slices.Make[byte](s.Alloc, int(size))
	used := 0
	for used < len(data) {
		n := int(km_own_bytes(f, data[used:]))
		if n <= 0 {
			break
		}
		used += n
	}
	valid := used == len(data) && km_own_stable(f)
	km_own_file_close(f)
	if !valid {
		slices.Free(s.Alloc, data)
		s.Status = -1
		return nil
	}
	s.Status = 0
	return data
}
