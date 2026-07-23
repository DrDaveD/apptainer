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
