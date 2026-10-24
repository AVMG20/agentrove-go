package sandbox

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
)

// Host-side helpers for box-writable directories. The agent is root in its
// box and can plant symlinks anywhere it can write, so these never act on a
// path string that was checked earlier: they work through an *os.Root (which
// refuses escaping symlinks and pins directories to fds) and never write
// through a symlink at the final component.

// ErrTooLarge is returned by ReadRegular for files above the size cap.
var ErrTooLarge = errors.New("file is too large")

// ReadRegular reads name under root only if it is a regular file (not a
// symlink, not a device or fifo) of at most max bytes.
func ReadRegular(root *os.Root, name string, max int64) ([]byte, error) {
	fi, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", name)
	}
	f, err := root.OpenFile(name, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	// Re-check the opened file: the entry may have been swapped since Lstat
	// (os.Root keeps that swap inside the root, but it could be a fifo).
	if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", name)
	}
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("%s: %w", name, ErrTooLarge)
	}
	return data, nil
}

// WriteFileAtomic writes data to name under root by creating a fresh,
// randomly named temp file next to it (O_EXCL) and renaming it into place.
// rename(2) replaces the directory entry itself, so a symlink planted at name
// is replaced, never written through, and readers never see a partial file.
// When uid > 0 and the API runs as root, the file is handed to that uid.
func WriteFileAtomic(root *os.Root, name string, data []byte, perm fs.FileMode, uid int) error {
	dir, base := path.Split(name)
	var rnd [8]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return err
	}
	tmp := dir + "." + base + ".agentrove-" + hex.EncodeToString(rnd[:]) + ".tmp"
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	_, werr := f.Write(data)
	if werr == nil {
		werr = f.Sync()
	}
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil && uid > 0 && os.Geteuid() == 0 {
		werr = root.Lchown(tmp, uid, uid)
	}
	if werr == nil {
		werr = root.Rename(tmp, name)
	}
	if werr != nil {
		_ = root.Remove(tmp)
		return werr
	}
	return nil
}

// WriteWorkspaceFile writes a file at the workspace-relative path name (e.g.
// an uploaded attachment), through the swap-safe workspace root and without
// following a symlink at name, creating missing parent dirs. In a Docker box
// run by a root API the new dirs and the file are handed to the sandbox user
// so the agent can edit them.
func (s *Sandbox) WriteWorkspaceFile(name string, data []byte) error {
	root, err := s.OpenWorkspaceRoot()
	if err != nil {
		return err
	}
	defer root.Close()
	uid := 0
	if s.Kind == KindDocker {
		uid = ContainerUID
	}
	cur := ""
	for _, part := range strings.Split(path.Dir(name), "/") {
		if part == "." || part == "" {
			continue
		}
		cur = path.Join(cur, part)
		if err := root.Mkdir(cur, 0o755); err == nil {
			if uid > 0 && os.Geteuid() == 0 {
				_ = root.Lchown(cur, uid, uid)
			}
		} else if !errors.Is(err, fs.ErrExist) {
			return err
		}
	}
	return WriteFileAtomic(root, name, data, 0o644, uid)
}
