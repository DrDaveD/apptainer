// Copyright (c) Contributors to the Apptainer project, established as
//   Apptainer a Series of LF Projects LLC.
//   For website terms of use, trademark policy, privacy policy and other
//   project policies see https://lfprojects.org/policies
// Copyright (c) 2018-2025, Sylabs Inc. All rights reserved.
// This software is licensed under a 3-clause BSD license. Please consult the
// LICENSE.md file distributed with the sources of this project regarding your
// rights to use or distribute this software.

package build

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/apptainer/apptainer/internal/pkg/build/oci"
	"github.com/apptainer/apptainer/internal/pkg/buildcfg"
	"github.com/apptainer/apptainer/pkg/build/types"
	"github.com/apptainer/apptainer/pkg/build/types/parser"
	"github.com/apptainer/apptainer/pkg/image"
	"github.com/apptainer/apptainer/pkg/inspect"
	"github.com/apptainer/apptainer/pkg/sylog"
)

func (s *stage) insertMetadata() error {
	// insert help
	if err := insertHelpScript(s.b); err != nil {
		return fmt.Errorf("while inserting help script: %v", err)
	}

	// insert labels
	if err := insertLabelsJSON(s.b); err != nil {
		return fmt.Errorf("while inserting labels json: %v", err)
	}

	// insert definition
	if err := insertDefinition(s.b); err != nil {
		return fmt.Errorf("while inserting definition: %v", err)
	}

	// insert environment
	if err := insertEnvScript(s.b); err != nil {
		return fmt.Errorf("while inserting environment script: %v", err)
	}

	// insert startscript
	if err := insertStartScript(s.b); err != nil {
		return fmt.Errorf("while inserting startscript: %v", err)
	}

	// insert runscript
	if err := insertRunScript(s.b); err != nil {
		return fmt.Errorf("while inserting runscript: %v", err)
	}

	// insert test script
	if err := insertTestScript(s.b); err != nil {
		return fmt.Errorf("while inserting test script: %v", err)
	}

	// insert JSON inspect metadata (must be the last call)
	if err := insertJSONInspectMetadata(s.b); err != nil {
		return fmt.Errorf("while inserting JSON inspect metadata: %v", err)
	}

	return nil
}

func insertEnvScript(b *types.Bundle) error {
	if b.RunSection("environment") && b.Recipe.ImageData.Environment.Script != "" {
		sylog.Infof("Adding environment to container")
		envScriptPath := filepath.Join(b.RootfsPath, "/.singularity.d/env/90-environment.sh")
		_, err := os.Stat(envScriptPath)
		if os.IsNotExist(err) {
			err := os.WriteFile(envScriptPath, []byte("#!/bin/sh\n\n"+b.Recipe.ImageData.Environment.Script+"\n"), 0o755)
			if err != nil {
				return err
			}
		} else {
			// append to script if it already exists
			f, err := os.OpenFile(envScriptPath, os.O_APPEND|os.O_WRONLY, 0o755)
			if err != nil {
				return err
			}
			defer f.Close()

			_, err = f.WriteString("\n" + b.Recipe.ImageData.Environment.Script + "\n")
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// runscript and startscript should use this function to properly handle args and shebangs
func handleShebangScript(s types.Script) (string, string) {
	shebang := "#!/bin/sh"
	script := ""
	if strings.HasPrefix(strings.TrimSpace(s.Script), "#!") {
		// separate and cleanup shebang
		split := strings.SplitN(s.Script, "\n", 2)
		shebang = strings.TrimSpace(split[0])
		if len(split) == 2 {
			script = split[1]
		}
	} else {
		script = s.Script
	}

	if s.Args != "" {
		// add arg after trimming comments
		shebang += " " + strings.Split(s.Args, "#")[0]
	}
	return shebang, script
}

func insertRunScript(b *types.Bundle) error {
	if b.RunSection("runscript") && b.Recipe.ImageData.Runscript.Script != "" {
		sylog.Infof("Adding runscript")
		shebang, script := handleShebangScript(b.Recipe.ImageData.Runscript)
		err := os.WriteFile(filepath.Join(b.RootfsPath, "/.singularity.d/runscript"), []byte(shebang+"\n\n"+script+"\n"), 0o755)
		if err != nil {
			return err
		}
	}
	return nil
}

func insertStartScript(b *types.Bundle) error {
	if b.RunSection("startscript") && b.Recipe.ImageData.Startscript.Script != "" {
		sylog.Infof("Adding startscript")
		shebang, script := handleShebangScript(b.Recipe.ImageData.Startscript)
		err := os.WriteFile(filepath.Join(b.RootfsPath, "/.singularity.d/startscript"), []byte(shebang+"\n\n"+script+"\n"), 0o755)
		if err != nil {
			return err
		}
	}
	return nil
}

func insertTestScript(b *types.Bundle) error {
	if b.RunSection("test") && b.Recipe.ImageData.Test.Script != "" {
		sylog.Infof("Adding testscript")
		shebang, script := handleShebangScript(b.Recipe.ImageData.Test)
		err := os.WriteFile(filepath.Join(b.RootfsPath, "/.singularity.d/test"), []byte(shebang+"\n\n"+script+"\n"), 0o755)
		if err != nil {
			return err
		}
	}
	return nil
}

func insertHelpScript(b *types.Bundle) error {
	if b.RunSection("help") && b.Recipe.ImageData.Help.Script != "" {
		_, err := os.Stat(filepath.Join(b.RootfsPath, "/.singularity.d/runscript.help"))
		if err != nil || b.Opts.Force {
			sylog.Infof("Adding help info")
			err := os.WriteFile(filepath.Join(b.RootfsPath, "/.singularity.d/runscript.help"), []byte(b.Recipe.ImageData.Help.Script+"\n"), 0o644)
			if err != nil {
				return err
			}
		} else {
			sylog.Warningf("Help message already exists and force option is false, not overwriting")
		}
	}
	return nil
}

func insertDefinition(b *types.Bundle) error {
	// Check for existing definition and move it to bootstrap history
	if _, err := os.Stat(filepath.Join(b.RootfsPath, "/.singularity.d/Singularity")); err == nil {
		// make bootstrap_history directory if it doesn't exist
		if _, err := os.Stat(filepath.Join(b.RootfsPath, "/.singularity.d/bootstrap_history")); err != nil {
			err = os.Mkdir(filepath.Join(b.RootfsPath, "/.singularity.d/bootstrap_history"), 0o755)
			if err != nil {
				return err
			}
		}

		// look at number of files in bootstrap_history to give correct file name
		files, err := os.ReadDir(filepath.Join(b.RootfsPath, "/.singularity.d/bootstrap_history"))
		if err != nil {
			return err
		}

		// name is "Apptainer" concatenated with an index based on number of other files in bootstrap_history
		histName := "Apptainer" + strconv.Itoa(len(files))
		// move old definition into bootstrap_history
		err = os.Rename(filepath.Join(b.RootfsPath, "/.singularity.d/Singularity"), filepath.Join(b.RootfsPath, "/.singularity.d/bootstrap_history", histName))
		if err != nil {
			return err
		}
	}

	err := os.WriteFile(filepath.Join(b.RootfsPath, "/.singularity.d/Singularity"), b.Recipe.FullRaw, 0o644)
	if err != nil {
		return err
	}

	return nil
}

func insertLabelsJSON(b *types.Bundle) (err error) {
	var text []byte
	labels := make(map[string]string)

	if err = getExistingLabels(labels, b); err != nil {
		return err
	}

	// get labels added through APPTAINER_LABELS environment variables
	buildLabels := filepath.Join(b.RootfsPath, sLabelsPath)
	content, err := os.ReadFile(buildLabels)
	if err == nil {
		if err := os.Remove(filepath.Join(b.RootfsPath, sLabelsPath)); err != nil {
			return err
		}
		for k, v := range parser.GetLabels(string(content)) {
			labels[k] = v
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("while reading %s: %s", buildLabels, err)
	}

	if err = addBuildLabels(labels, b); err != nil {
		return err
	}

	if b.RunSection("labels") && len(b.Recipe.ImageData.Labels) > 0 {
		sylog.Infof("Adding labels")

		// add new labels to new map and check for collisions
		for key, value := range b.Recipe.ImageData.Labels {
			// check if label already exists
			if _, ok := labels[key]; ok {
				// overwrite collision if it exists and force flag is set
				if b.Opts.Force {
					labels[key] = value
				} else {
					sylog.Warningf("Label: %s already exists and force option is false, not overwriting", key)
				}
			} else {
				// set if it doesn't
				labels[key] = value
			}
		}
	}

	// make new map into json
	text, err = json.MarshalIndent(labels, "", "\t")
	if err != nil {
		return err
	}

	err = os.WriteFile(filepath.Join(b.RootfsPath, "/.singularity.d/labels.json"), []byte(text), 0o644)
	return err
}

func insertJSONInspectMetadata(b *types.Bundle) error {
	metadata := new(inspect.Metadata)

	exe := filepath.Join(buildcfg.BINDIR, "apptainer")
	cmd := exec.Command(exe, "inspect", "--all", b.RootfsPath)
	cmd.Stderr = os.Stderr

	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("while executing inspect command: %s", err)
	}

	if err := json.Unmarshal(out, metadata); err != nil {
		return fmt.Errorf("while decoding inspect metadata: %s", err)
	}
	data, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("while encoding inspect metadata: %s", err)
	}

	b.JSONObjects[image.SIFDescInspectMetadataJSON] = data

	return nil
}

func getExistingLabels(labels map[string]string, b *types.Bundle) error {
	// check for existing labels in bundle
	if _, err := os.Stat(filepath.Join(b.RootfsPath, "/.singularity.d/labels.json")); err == nil {
		jsonFile, err := os.Open(filepath.Join(b.RootfsPath, "/.singularity.d/labels.json"))
		if err != nil {
			return err
		}
		defer jsonFile.Close()

		jsonBytes, err := io.ReadAll(jsonFile)
		if err != nil {
			return err
		}

		err = json.Unmarshal(jsonBytes, &labels)
		if err != nil {
			return err
		}
	}
	return nil
}

func addBuildLabels(labels map[string]string, b *types.Bundle) error {
	// schema version
	labels["org.label-schema.schema-version"] = "1.0"

	// build date and time, lots of time formatting
	currentTime := time.Now()
	year, month, day := currentTime.Date()
	date := strconv.Itoa(day) + `_` + month.String() + `_` + strconv.Itoa(year)
	hours, minutes, secs := currentTime.Clock()
	time := strconv.Itoa(hours) + `:` + strconv.Itoa(minutes) + `:` + strconv.Itoa(secs)
	zone, _ := currentTime.Zone()
	timeString := currentTime.Weekday().String() + `_` + date + `_` + time + `_` + zone
	labels["org.label-schema.build-date"] = timeString

	// apptainer version
	labels["org.label-schema.usage.apptainer.version"] = buildcfg.PACKAGE_VERSION

	// help info if help exists in the definition and is run in the build
	if b.RunSection("help") && b.Recipe.ImageData.Help.Script != "" {
		labels["org.label-schema.usage"] = "/.singularity.d/runscript.help"
		labels["org.label-schema.usage.apptainer.runscript.help"] = "/.singularity.d/runscript.help"
	}

	// bootstrap header info, only if this build actually bootstrapped
	if !b.Opts.Update || b.Opts.Force {
		for key, value := range b.Recipe.Header {
			labels["org.label-schema.usage.singularity.deffile."+key] = value
		}
	}

	// Digest of image
	if b.Opts.Tag != "" && b.Opts.Digest != "" {
		labels["org.opencontainers.image.base.name"] = b.Opts.Tag
		labels["org.opencontainers.image.base.digest"] = b.Opts.Digest
	}

	// Architecture of build
	buildarch := oci.ArchMap[b.Opts.Arch]
	labels["org.label-schema.build-arch"] = buildarch.Arch

	return nil
}
