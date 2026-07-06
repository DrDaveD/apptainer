// Copyright (c) Contributors to the Apptainer project, established as
//   Apptainer a Series of LF Projects LLC.
//   For website terms of use, trademark policy, privacy policy and other
//   project policies see https://lfprojects.org/policies

package basepath

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindBaseImage(t *testing.T) {
	tmp := t.TempDir()

	hash := "abcdef0123456789"

	// directDir is a plain directory in the basepath that is not itself a
	// sandbox image, and does not directly hold the hash.
	directDir := filepath.Join(tmp, "empty")
	if err := os.Mkdir(directDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// flatDir holds the hash directly.
	flatDir := filepath.Join(tmp, "flat")
	if err := os.Mkdir(flatDir, 0o755); err != nil {
		t.Fatal(err)
	}
	flatMatch := filepath.Join(flatDir, hash)
	if err := os.WriteFile(flatMatch, []byte("fake sif"), 0o644); err != nil {
		t.Fatal(err)
	}

	// fanoutDir holds the hash within a subdirectory named with the first
	// two characters of the hash.
	fanoutDir := filepath.Join(tmp, "fanout")
	if err := os.MkdirAll(filepath.Join(fanoutDir, hash[:2]), 0o755); err != nil {
		t.Fatal(err)
	}
	fanoutMatch := filepath.Join(fanoutDir, hash[:2], hash)
	if err := os.WriteFile(fanoutMatch, []byte("fake sif"), 0o644); err != nil {
		t.Fatal(err)
	}

	// directImage is a path that is itself a sandbox image and should be
	// used unconditionally regardless of hash.
	directImage := filepath.Join(tmp, "directimage")
	if err := os.MkdirAll(filepath.Join(directImage, ".singularity.d"), 0o755); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		paths   []string
		hash    string
		want    string
		wantErr bool
	}{
		{
			name:  "flat match",
			paths: []string{directDir, flatDir},
			hash:  hash,
			want:  flatMatch,
		},
		{
			name:  "fanout match",
			paths: []string{directDir, fanoutDir},
			hash:  hash,
			want:  fanoutMatch,
		},
		{
			name:  "prefer flat before fanout when both given first",
			paths: []string{flatDir, fanoutDir},
			hash:  hash,
			want:  flatMatch,
		},
		{
			name:  "direct image path used regardless of hash",
			paths: []string{directImage, flatDir},
			hash:  "some-other-hash",
			want:  directImage,
		},
		{
			name:    "not found",
			paths:   []string{directDir},
			hash:    hash,
			wantErr: true,
		},
		{
			name:    "empty basepath",
			paths:   nil,
			hash:    hash,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := FindBaseImage(tt.hash, tt.paths)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got result %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}
