package transcode

import (
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

// Advisory locks coordinate all plugin processes sharing the Silo container.
// The kernel releases them on crashes, so a dead plugin cannot strand capacity.
func acquireGPU(root string) (*os.File, error) {
	if root == "" {
		root = filepath.Join(os.TempDir(), "silo-livetv-gpu")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, ErrUnavailable
	}
	for slot := 0; slot < 2; slot++ {
		file, err := os.OpenFile(filepath.Join(root, "slot-"+strconv.Itoa(slot)), os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			return nil, ErrUnavailable
		}
		if syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil {
			return file, nil
		}
		file.Close()
	}
	return nil, ErrCapacity
}
