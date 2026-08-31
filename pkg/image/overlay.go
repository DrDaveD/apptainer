// Copyright (c) Contributors to the Apptainer project, established as
//   Apptainer a Series of LF Projects LLC.
//   For website terms of use, trademark policy, privacy policy and other
//   project policies see https://lfprojects.org/policies

package image

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/apptainer/apptainer/pkg/inspect"
)

// OverlayBaseHashFile is the path, relative to the root of an overlay-only
// image built with `apptainer build --overlay`, of the file holding the
// hash of the base image it was built on top of.
const OverlayBaseHashFile = ".singularity.d/overlay-basehash"

func GetOverlayBaseHash(path string) (hash string, ok bool, err error) {
	img, err := Init(path, false)
	if err != nil {
		return "", false, err
	}
	defer img.File.Close()

	switch img.Type {
	case SANDBOX:
		labelsPath := filepath.Join(img.Path, ".singularity.d/labels.json")
		data, err := os.ReadFile(labelsPath)
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		} else if err != nil {
			return "", false, err
		}
		var labels map[string]string
		if err := json.Unmarshal(data, &labels); err != nil {
			return "", false, err
		}
		if h, ok := labels["org.label-schema.usage.singularity.overlay.base-hash"]; ok {
			return strings.TrimSpace(h), true, nil
		}
		return "", false, nil
	case SIF:
		r, err := NewSectionReader(img, SIFDescInspectMetadataJSON, -1)
		if errors.Is(err, ErrNoSection) {
			return "", false, nil
		} else if err != nil {
			return "", false, err
		}
		metadata := new(inspect.Metadata)
		if err := json.NewDecoder(r).Decode(metadata); err != nil {
			return "", false, err
		}
		if h, ok := metadata.Attributes.Labels["org.label-schema.usage.singularity.overlay.base-hash"]; ok {
			return strings.TrimSpace(h), true, nil
		}
		return "", false, nil
	default:
		return "", false, nil
	}
}
