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
	"os/user"
	"path/filepath"
	"syscall"

	"github.com/apptainer/apptainer/pkg/image"
)

// OverlayMount represents an overlayfs mount used for build --overlay.
// It tracks the base, upper, work, and merged directories so they can be
// cleaned up after the build completes.
type OverlayMount struct {
	BaseLower string // path to read-only base image rootfs
	UpperDir  string // path to writable upper layer
	WorkDir   string // overlayfs work directory
	MergedDir string // path where overlayfs is mounted (the build rootfs)
}

// SetupOverlayMount sets up overlayfs for a build, with the base image as a
// read-only lower layer and a writable upper layer for build changes.
// Returns an OverlayMount structure to track the directories for cleanup.
func SetupOverlayMount(basePath, tmpDir string) (*OverlayMount, error) {
	// Check if running as root (user id 0)
	currentUser, err := user.Current()
	if err != nil {
		return nil, fmt.Errorf("failed to get current user: %w", err)
	}
	if currentUser.Uid != "0" {
		return nil, fmt.Errorf("'build --overlay' requires root privileges (current uid: %s)", currentUser.Uid)
	}

	// Create directories for overlay: parent -> lower (base), upper, work, merged
	overlayParent, err := os.MkdirTemp(tmpDir, "overlay-")
	if err != nil {
		return nil, fmt.Errorf("failed to create overlay parent directory: %w", err)
	}

	baseLower := filepath.Join(overlayParent, "lower")
	upperDir := filepath.Join(overlayParent, "upper")
	workDir := filepath.Join(overlayParent, "work")
	mergedDir := filepath.Join(overlayParent, "merged")

	// Create directories
	for _, dir := range []string{baseLower, upperDir, workDir, mergedDir} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			os.RemoveAll(overlayParent)
			return nil, fmt.Errorf("failed to create %s: %w", dir, err)
		}
	}

	// Move base image rootfs to lower directory
	// This extracts all files from basePath into baseLower
	if err := copyDirContents(basePath, baseLower); err != nil {
		os.RemoveAll(overlayParent)
		return nil, fmt.Errorf("failed to copy base image to lower layer: %w", err)
	}

	// Mount overlayfs: lower (base) is read-only, upper/work are writable
	options := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s", baseLower, upperDir, workDir)
	if err := syscall.Mount("overlay", mergedDir, "overlay", 0, options); err != nil {
		os.RemoveAll(overlayParent)
		return nil, fmt.Errorf("failed to mount overlayfs at %s: %w", mergedDir, err)
	}

	return &OverlayMount{
		BaseLower: baseLower,
		UpperDir:  upperDir,
		WorkDir:   workDir,
		MergedDir: mergedDir,
	}, nil
}

// TeardownOverlayMount unmounts the overlayfs and returns the path to the upper
// directory containing only the changes. The parent overlay directory is NOT
// removed, allowing the upper directory contents to be used for the final image.
func TeardownOverlayMount(om *OverlayMount) (string, error) {
	if om == nil {
		return "", fmt.Errorf("overlay mount is nil")
	}

	// Unmount the overlayfs
	if err := syscall.Unmount(om.MergedDir, 0); err != nil {
		return "", fmt.Errorf("failed to unmount overlayfs at %s: %w", om.MergedDir, err)
	}

	// Return the path to the upper directory (contains only the changes)
	return om.UpperDir, nil
}

// copyDirContents recursively copies the contents of src to dst.
// This is used to copy the base image into the lower layer of the overlay.
func copyDirContents(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		if rel == "." {
			return nil
		}

		dstPath := filepath.Join(dst, rel)

		if info.IsDir() {
			return os.Mkdir(dstPath, info.Mode())
		}

		// Create parent directories if needed
		if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
			return err
		}

		if info.Mode()&os.ModeSymlink != 0 {
			// Copy symlink
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(target, dstPath)
		}

		// Copy regular file
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()

		out, err := os.OpenFile(dstPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
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

		return os.Chtimes(dstPath, info.ModTime(), info.ModTime())
	})
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
