// Copyright (c) Contributors to the Apptainer project, established as
//   Apptainer a Series of LF Projects LLC.
//   For website terms of use, trademark policy, privacy policy and other
//   project policies see https://lfprojects.org/policies
// This software is licensed under a 3-clause BSD license. Please consult the
// LICENSE.md file distributed with the sources of this project regarding your
// rights to use or distribute this software.

package overlayfsfuse

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/apptainer/apptainer/internal/pkg/util/bin"
	"github.com/apptainer/apptainer/pkg/image"
	"github.com/apptainer/apptainer/pkg/sylog"
	"github.com/apptainer/apptainer/pkg/util/capabilities"
	"github.com/apptainer/apptainer/pkg/util/fs/proc"
)

const (
	driverName = "overlayfsfuse"
	binName    = "fuse-overlayfs"
)

type overlayfsfuseDriver struct {
	cmd     *exec.Cmd
	cmdpath string
}

func Init(register bool, desiredFeatures image.DriverFeature) (bool, error) {
	binPath, err := bin.FindBin(binName)
	if err != nil {
		sylog.Debugf("%v driver not enabled because: %v", driverName, err)
		if (desiredFeatures & image.OverlayFeature) != 0 {
			// don't say that overlay won't work because it
			//  might still work on a new-enough kernel
			sylog.Infof("%v not found", binName)
		}
		return false, nil
	}
	if !register {
		return true, nil
	}
	sylog.Debugf("Registering Driver %v", driverName)
	return true, image.RegisterDriver(driverName, &overlayfsfuseDriver{nil, binPath})
}

func (d *overlayfsfuseDriver) Features() image.DriverFeature {
	return image.OverlayFeature
}

func (d *overlayfsfuseDriver) Mount(params *image.MountParams, _ image.MountFunc) error {
	optsStr := strings.Join(params.FSOptions, ",")
	d.cmd = exec.Command(d.cmdpath, "-f", "-o", optsStr, params.Target)
	sylog.Debugf("Executing %v", d.cmd.String())
	var stderr bytes.Buffer
	d.cmd.Stderr = &stderr
	d.cmd.SysProcAttr = &syscall.SysProcAttr{
		AmbientCaps: []uintptr{
			uintptr(capabilities.Map["CAP_SYS_ADMIN"].Value),
		},
	}
	var err error
	if err = d.cmd.Start(); err != nil {
		return fmt.Errorf("%v Start failed: %v: %v", binName, err, stderr.String())
	}
	process := d.cmd.Process
	if process == nil {
		return fmt.Errorf("no %v process started", binName)
	}
	maxTime := 2 * time.Second
	totTime := 0 * time.Second
	for totTime < maxTime {
		sleepTime := 25 * time.Millisecond
		time.Sleep(sleepTime)
		totTime += sleepTime
		err = process.Signal(os.Signal(syscall.Signal(0)))
		if err != nil {
			err := d.cmd.Wait()
			return fmt.Errorf("%v failed: %v: %v", binName, err, stderr.String())
		}
		entries, err := proc.GetMountInfoEntry("/proc/self/mountinfo")
		if err != nil {
			d.Stop()
			return fmt.Errorf("%v failure to get mount info: %v", binName, err)
		}
		for _, entry := range entries {
			if entry.Point == params.Target {
				sylog.Debugf("%v mounted in %v", params.Target, totTime)
				return nil
			}
		}
	}
	d.Stop()
	return fmt.Errorf("%v failed to mount %v in %v", binName, params.Target, maxTime)
}

func (d *overlayfsfuseDriver) Start(_ *image.DriverParams) error {
	return nil
}

func (d *overlayfsfuseDriver) Stop() error {
	if d.cmd != nil {
		process := d.cmd.Process
		if process != nil {
			sylog.Debugf("Killing %v", binName)
			process.Kill()
		}
	}
	return nil
}
