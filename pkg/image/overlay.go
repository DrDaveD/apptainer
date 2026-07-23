// Copyright (c) Contributors to the Apptainer project, established as
//   Apptainer a Series of LF Projects LLC.
//   For website terms of use, trademark policy, privacy policy and other
//   project policies see https://lfprojects.org/policies

package image

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// OverlayBaseHashFile is the path, relative to the root of an overlay-only
// image built with `apptainer build --overlay`, of the file holding the
// hash of the base image it was built on top of. It is written into sandbox
// images, and mirrored as a SIF descriptor (SIFDescOverlayBaseHash) for SIF
// images since a SIF's squashfs partition content cannot be read without
// first extracting it.
const OverlayBaseHashFile = ".singularity.d/overlay-basehash"

// GetOverlayBaseHash returns the hash of the base image that the image at
// path was built on top of, using `apptainer build --overlay`. ok is false
// if the image is not an overlay-only image, i.e. it was not built with
// `apptainer build --overlay`.
func GetOverlayBaseHash(path string) (hash string, ok bool, err error) {
	img, err := Init(path, false)
	if err != nil {
		return "", false, err
	}
	defer img.File.Close()

	switch img.Type {
	case SANDBOX:
		data, err := os.ReadFile(filepath.Join(img.Path, OverlayBaseHashFile))
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		} else if err != nil {
			return "", false, err
		}
		return strings.TrimSpace(string(data)), true, nil
	case SIF:
		r, err := NewSectionReader(img, SIFDescOverlayBaseHash, -1)
		if errors.Is(err, ErrNoSection) {
			return "", false, nil
		} else if err != nil {
			return "", false, err
		}
		data, err := io.ReadAll(r)
		if err != nil {
			return "", false, err
		}
		var h string
		if err := json.Unmarshal(data, &h); err != nil {
			return "", false, err
		}
		return strings.TrimSpace(h), true, nil
	default:
		return "", false, nil
	}
}
