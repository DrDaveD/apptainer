// Copyright (c) Contributors to the Apptainer project, established as
//   Apptainer a Series of LF Projects LLC.
//   For website terms of use, trademark policy, privacy policy and other
//   project policies see https://lfprojects.org/policies
// This software is licensed under a 3-clause BSD license. Please consult the
// LICENSE.md file distributed with the sources of this project regarding your
// rights to use or distribute this software.

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
// It tracks the upper, work, and merged directories
type OverlayMount struct {
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

	// Create directories for overlay: upper, work, merged
	overlayParent, err := os.MkdirTemp(tmpDir, "overlay-")
	if err != nil {
		return nil, fmt.Errorf("failed to create overlay parent directory: %w", err)
	}

	upperDir := filepath.Join(overlayParent, "upper")
	workDir := filepath.Join(overlayParent, "work")
	mergedDir := filepath.Join(overlayParent, "merged")

	// Create directories
	for _, dir := range []string{upperDir, workDir, mergedDir} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			os.RemoveAll(overlayParent)
			return nil, fmt.Errorf("failed to create %s: %w", dir, err)
		}
	}

	// Mount overlayfs: basePath (base image) is used directly as lower layer, upper/work are writable
	options := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s", basePath, upperDir, workDir)
	if err := syscall.Mount("overlay", mergedDir, "overlay", 0, options); err != nil {
		os.RemoveAll(overlayParent)
		return nil, fmt.Errorf("failed to mount overlayfs at %s: %w", mergedDir, err)
	}

	return &OverlayMount{
		UpperDir:  upperDir,
		WorkDir:   workDir,
		MergedDir: mergedDir,
	}, nil
}

// TeardownOverlayMount unmounts the overlayfs and returns the path to the overlay parent
// directory which contains both the upper and work subdirectories. This is the format
// expected by the apptainer run/exec/shell --overlay option.
func TeardownOverlayMount(om *OverlayMount) (string, error) {
	if om == nil {
		return "", fmt.Errorf("overlay mount is nil")
	}

	// Unmount the overlayfs
	if err := syscall.Unmount(om.MergedDir, 0); err != nil {
		return "", fmt.Errorf("failed to unmount overlayfs at %s: %w", om.MergedDir, err)
	}

	// Clean out the merged dir
	if err := syscall.Rmdir(om.MergedDir); err != nil {
		return "", fmt.Errorf("Removing %v did not succeed because: %v", om.MergedDir, err)
	}

	// Return the path to the overlay parent directory (contains both upper and work)
	return filepath.Dir(om.UpperDir), nil
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
