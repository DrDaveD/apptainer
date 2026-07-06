// Copyright (c) Contributors to the Apptainer project, established as
//   Apptainer a Series of LF Projects LLC.
//   For website terms of use, trademark policy, privacy policy and other
//   project policies see https://lfprojects.org/policies

package build

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/apptainer/apptainer/pkg/image"
	"github.com/apptainer/apptainer/pkg/sylog"
)

// overlayFileState records enough information about a rootfs entry to
// detect whether it was added, removed, or modified between two snapshots
// of a build's root filesystem.
type overlayFileState struct {
	mode  os.FileMode
	size  int64
	mtime int64
	link  string
	isDir bool
}

// snapshotRootfs walks root and returns a map of relative path to state,
// used later to compute which entries were added, changed, or removed by
// the build process, for `apptainer build --overlay`.
func snapshotRootfs(root string) (map[string]overlayFileState, error) {
	snapshot := make(map[string]overlayFileState)

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}

		state := overlayFileState{
			mode:  info.Mode(),
			size:  info.Size(),
			mtime: info.ModTime().UnixNano(),
			isDir: info.IsDir(),
		}
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			state.link = link
		}

		snapshot[rel] = state
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("while walking %s: %w", root, err)
	}

	return snapshot, nil
}

// buildOverlayDiff compares the current content of root against base (a
// snapshot taken with snapshotRootfs before the build ran), and populates
// destDir with only the added or changed entries, plus whiteout character
// devices (following the overlayfs convention: a character device with
// major/minor 0/0) for entries that were removed.
func buildOverlayDiff(root string, base map[string]overlayFileState, destDir string) error {
	seen := make(map[string]bool, len(base))

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		seen[rel] = true

		prev, existed := base[rel]
		changed := !existed || overlayEntryChanged(path, prev, info)

		dst := filepath.Join(destDir, rel)

		if info.IsDir() {
			if err := os.MkdirAll(dst, 0o700); err != nil {
				return err
			}
			if changed {
				return os.Chmod(dst, info.Mode().Perm())
			}
			return nil
		}

		if !changed {
			return nil
		}

		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return err
		}

		return copyOverlayEntry(path, dst, info)
	})
	if err != nil {
		return fmt.Errorf("while computing overlay diff of %s: %w", root, err)
	}

	// Anything present in the base snapshot, but no longer present in the
	// final rootfs, must be recorded as a whiteout in the overlay.
	for rel := range base {
		if seen[rel] {
			continue
		}
		// Skip entries whose parent was itself removed; the parent
		// whiteout is sufficient.
		if parentRemoved(rel, base, seen) {
			continue
		}
		dst := filepath.Join(destDir, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return err
		}
		if err := syscall.Mknod(dst, syscall.S_IFCHR|0o000, 0); err != nil {
			return fmt.Errorf("while creating whiteout for %s: %w", rel, err)
		}
	}

	return nil
}

// parentRemoved returns true if any parent directory of rel was itself
// removed (i.e. is present in base but not in seen).
func parentRemoved(rel string, base map[string]overlayFileState, seen map[string]bool) bool {
	dir := filepath.Dir(rel)
	for dir != "." && dir != "/" {
		if _, existed := base[dir]; existed && !seen[dir] {
			return true
		}
		dir = filepath.Dir(dir)
	}
	return false
}

func overlayEntryChanged(path string, prev overlayFileState, info os.FileInfo) bool {
	if prev.isDir != info.IsDir() {
		return true
	}
	if prev.mode != info.Mode() {
		return true
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(path)
		if err != nil {
			return true
		}
		return target != prev.link
	}
	if !info.IsDir() {
		if prev.size != info.Size() {
			return true
		}
		if prev.mtime != info.ModTime().UnixNano() {
			return true
		}
	}
	return false
}

// copyOverlayEntry copies a single non-directory entry (regular file,
// symlink, or other special file) from src to dst, preserving mode and, for
// regular files, modification time.
func copyOverlayEntry(src, dst string, info os.FileInfo) error {
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		if err := os.RemoveAll(dst); err != nil {
			return err
		}
		return os.Symlink(target, dst)
	case info.Mode().IsRegular():
		return copyRegularFile(src, dst, info)
	default:
		sylog.Debugf("Skipping special file %s in overlay diff", src)
		return nil
	}
}

func copyRegularFile(src, dst string, info os.FileInfo) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.RemoveAll(dst); err != nil {
		return err
	}

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}

	return os.Chtimes(dst, info.ModTime(), info.ModTime())
}

// hashBaseImage computes a sha256 hash of the base image file, used to tag
// an overlay-only build so its base can be found at runtime with
// `--basepath`/`APPTAINER_BASEPATH`.
func hashBaseImage(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("'build --overlay' does not support a sandbox base image %q; the base must be a SIF file", path)
	}

	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// writeOverlayBaseHash writes the overlay-basehash marker file into rootfs,
// so a sandbox produced from it (or the final squashed SIF) carries the
// hash of the base image it was built on top of.
func writeOverlayBaseHash(rootfs, hash string) error {
	path := filepath.Join(rootfs, image.OverlayBaseHashFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(hash+"\n"), 0o644)
}
