package transcode

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

func diskAvailable(root string) uint64 {
	var st syscall.Statfs_t
	if syscall.Statfs(root, &st) != nil {
		return 0
	}
	return uint64(st.Bavail) * uint64(st.Bsize)
}

func cleanStaleRoots(root string) {
	if root == "" {
		root = os.TempDir()
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "silo-live-media-") {
			continue
		}
		parts := strings.Split(strings.TrimPrefix(entry.Name(), "silo-live-media-"), "-")
		if len(parts) < 2 {
			continue
		}
		pid, err := strconv.Atoi(parts[0])
		if err != nil || pid <= 0 {
			continue
		}
		if syscall.Kill(pid, 0) == syscall.ESRCH {
			os.RemoveAll(filepath.Join(root, entry.Name()))
		}
	}
}
