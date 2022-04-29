// Copyright (c) Contributors to the Apptainer project, established as
//   Apptainer a Series of LF Projects LLC.
//   For website terms of use, trademark policy, privacy policy and other
//   project policies see https://lfprojects.org/policies
// This software is licensed under a 3-clause BSD license. Please consult the
// LICENSE.md file distributed with the sources of this project regarding your
// rights to use or distribute this software.

package squashfuse

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path"
	"strconv"
	"syscall"
	"time"

	"github.com/apptainer/apptainer/internal/pkg/util/bin"
	"github.com/apptainer/apptainer/pkg/image"
	"github.com/apptainer/apptainer/pkg/sylog"
	"github.com/apptainer/apptainer/pkg/util/capabilities"
	"github.com/apptainer/apptainer/pkg/util/fs/proc"
)

const (
	driverName = "squashfuse"
	binName    = "squashfuse"
)

type squashfuseDriver struct {
	cmd     *exec.Cmd
	cmdpath string
}

func Init(register bool, desiredFeatures image.DriverFeature) (bool, error) {
	binPath, err := bin.FindBin(binName)
	if err != nil {
		sylog.Debugf("%v driver not enabled because: %v", driverName, err)
		if (desiredFeatures & image.ImageFeature) != 0 {
			sylog.Infof("%v not found, will not be able to mount SIF", binName)
		}
		return false, nil
	}
	if !register {
		return true, nil
	}
	sylog.Debugf("Registering Driver %v", driverName)
	return true, image.RegisterDriver(driverName, &squashfuseDriver{nil, binPath})
}

func (d *squashfuseDriver) Features() image.DriverFeature {
	return image.ImageFeature
}

func (d *squashfuseDriver) Mount(params *image.MountParams, _ image.MountFunc) error {
	optsStr := "offset=" + strconv.FormatUint(params.Offset, 10)
	srcPath := params.Source
	if path.Dir(params.Source) == "/proc/self/fd" {
		// this will be passed as the first ExtraFile below, always fd 3
		srcPath = "/proc/self/fd/3"
	}
	d.cmd = exec.Command(d.cmdpath, "-f", "-o", optsStr, srcPath, params.Target)
	sylog.Debugf("Executing %v", d.cmd.String())
	var stderr bytes.Buffer
	d.cmd.Stderr = &stderr
	if path.Dir(params.Source) == "/proc/self/fd" {
		d.cmd.ExtraFiles = make([]*os.File, 1)
		targetFd, _ := strconv.Atoi(path.Base(params.Source))
		d.cmd.ExtraFiles[0] = os.NewFile(uintptr(targetFd), params.Source)
	}
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

func (d *squashfuseDriver) Start(_ *image.DriverParams) error {
	return nil
}

func (d *squashfuseDriver) Stop() error {
	if d.cmd != nil {
		process := d.cmd.Process
		if process != nil {
			sylog.Debugf("Killing %v", binName)
			process.Kill()
		}
	}
	return nil
}
