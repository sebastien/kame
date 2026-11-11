package posix

import (
	"errors"
	"kame/core"
	"kame/host"
	"kame/internal/c"
	"os"
	"syscall"
)

func km_file_publish(staged, destination string) c.Int {
	info, e := os.Lstat(staged)
	if e != nil || !info.Mode().IsRegular() {
		return -1
	}
	if e := os.Link(staged, destination); e != nil {
		if errors.Is(e, syscall.EXDEV) || errors.Is(e, syscall.ENOTSUP) {
			return 3
		}
		if errors.Is(e, os.ErrExist) {
			return 1
		}
		return -1
	}
	if os.Remove(staged) != nil {
		return 2
	}
	return 0
}

func (h *Host) PublishFile(staged string, destination string) host.PublishResult {
	if core.IsMemoryResourceURIName(staged) || core.IsMemoryResourceURIName(destination) {
		return host.PublishResult{Code: "FS_UNSUPPORTED"}
	}
	source, target := h.hostPath(staged), h.hostPath(destination)
	defer h.freeHostPath(source)
	defer h.freeHostPath(target)
	if source.Error != nil || target.Error != nil {
		return host.PublishResult{Code: "FS_ERR"}
	}
	switch km_file_publish(source.Path, target.Path) {
	case 0:
		return host.PublishResult{Published: true, StagedRemoved: true}
	case 1:
		return host.PublishResult{Code: "FS_EXISTS"}
	case 2:
		return host.PublishResult{Code: "FS_PUBLISHED", Published: true}
	case 3:
		return host.PublishResult{Code: "FS_UNSUPPORTED"}
	}
	return host.PublishResult{Code: "FS_ERR"}
}
