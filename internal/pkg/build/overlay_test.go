// Copyright (c) Contributors to the Apptainer project, established as
//   Apptainer a Series of LF Projects LLC.
//   For website terms of use, trademark policy, privacy policy and other
//   project policies see https://lfprojects.org/policies

package build

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/apptainer/apptainer/pkg/image"
	"gotest.tools/v3/assert"
)

func TestSnapshotAndBuildOverlayDiff(t *testing.T) {
	root := t.TempDir()

	mustWriteFile(t, filepath.Join(root, "etc", "basefile"), "base\n")
	mustWriteFile(t, filepath.Join(root, "etc", "unchanged"), "same\n")
	mustMkdirAll(t, filepath.Join(root, "opt"))

	snapshot, err := snapshotRootfs(root)
	assert.NilError(t, err)
	assert.Assert(t, len(snapshot) > 0)

	// Simulate the build's %post: remove basefile, add a new file/dir,
	// leave "unchanged" alone.
	assert.NilError(t, os.Remove(filepath.Join(root, "etc", "basefile")))
	mustMkdirAll(t, filepath.Join(root, "opt", "newdir"))
	mustWriteFile(t, filepath.Join(root, "opt", "newdir", "file"), "new\n")
	mustWriteFile(t, filepath.Join(root, "etc", "overlayfile"), "overlay content\n")

	destDir := t.TempDir()
	assert.NilError(t, buildOverlayDiff(root, snapshot, destDir))

	// Added file present in diff.
	added, err := os.ReadFile(filepath.Join(destDir, "etc", "overlayfile"))
	assert.NilError(t, err)
	assert.Equal(t, string(added), "overlay content\n")

	// New directory's new file present in diff.
	newFile, err := os.ReadFile(filepath.Join(destDir, "opt", "newdir", "file"))
	assert.NilError(t, err)
	assert.Equal(t, string(newFile), "new\n")

	// Unchanged file must not be present in the diff.
	_, err = os.Stat(filepath.Join(destDir, "etc", "unchanged"))
	assert.Assert(t, os.IsNotExist(err))

	// Removed file must have a whiteout char device with major/minor 0/0.
	whiteoutPath := filepath.Join(destDir, "etc", "basefile")
	info, err := os.Lstat(whiteoutPath)
	assert.NilError(t, err)
	assert.Assert(t, info.Mode()&os.ModeCharDevice != 0, "expected whiteout to be a char device")
}

func TestHashBaseImage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "base.sif")
	mustWriteFile(t, path, "some sif content")

	hash, err := hashBaseImage(path)
	assert.NilError(t, err)
	assert.Equal(t, hash, "e2d30bf8186c9e6a2506891ec08a67d730700522756ef5cce23f00887402985b")

	_, err = hashBaseImage(dir)
	assert.ErrorContains(t, err, "does not support a sandbox base image")
}

func TestWriteOverlayBaseHash(t *testing.T) {
	rootfs := t.TempDir()
	hash := "abc123"

	assert.NilError(t, writeOverlayBaseHash(rootfs, hash))

	content, err := os.ReadFile(filepath.Join(rootfs, image.OverlayBaseHashFile))
	assert.NilError(t, err)
	assert.Equal(t, string(content), hash+"\n")
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	assert.NilError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	assert.NilError(t, os.WriteFile(path, []byte(content), 0o644))
}

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	assert.NilError(t, os.MkdirAll(path, 0o755))
}
