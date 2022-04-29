// Copyright (c) Contributors to the Apptainer project, established as
//   Apptainer a Series of LF Projects LLC.
//   For website terms of use, trademark policy, privacy policy and other
//   project policies see https://lfprojects.org/policies
// This software is licensed under a 3-clause BSD license. Please consult the
// LICENSE.md file distributed with the sources of this project regarding your
// rights to use or distribute this software.

package driver

import (
	"fmt"

	"github.com/apptainer/apptainer/internal/pkg/image/driver/overlayfsfuse"
	"github.com/apptainer/apptainer/internal/pkg/image/driver/squashfuse"
	"github.com/apptainer/apptainer/pkg/image"
	"github.com/apptainer/apptainer/pkg/sylog"
	"github.com/apptainer/apptainer/pkg/util/apptainerconf"
)

const driverName = "fuseapps"

type fuseappsDriver struct {
	squashImageDriver    image.Driver
	overlayfsImageDriver image.Driver
}

func InitImageDrivers(register bool, unprivileged bool, fileconf *apptainerconf.File, desiredFeatures image.DriverFeature) error {
	if fileconf.ImageDriver != "" && fileconf.ImageDriver != driverName {
		sylog.Debugf("skipping installing %v image drivers because %v already configured", driverName, fileconf.ImageDriver)
		// allow a configured driver to take precedence
		return nil
	}
	if !unprivileged {
		// no need for these drivers if running privileged
		if fileconf.ImageDriver == driverName {
			// must have been incorrectly thought to be unprivileged
			// at an earlier point (e.g. TestLibraryPacker unit-test)
			fileconf.ImageDriver = ""
		}
		return nil
	}
	squashactive, err := squashfuse.Init(register, desiredFeatures)
	if err != nil {
		return fmt.Errorf("error initializing squashfuse driver: %v", err)
	}
	overlayactive, err := overlayfsfuse.Init(register, desiredFeatures)
	if err != nil {
		return fmt.Errorf("error initializing overlayfsfuse driver: %v", err)
	}
	if squashactive || overlayactive {
		sylog.Debugf("Setting ImageDriver to %v", driverName)
		fileconf.ImageDriver = driverName
		return image.RegisterDriver(driverName, &fuseappsDriver{})
	}
	return nil
}

func (d *fuseappsDriver) Features() image.DriverFeature {
	var features image.DriverFeature
	d.squashImageDriver = image.GetDriver("squashfuse")
	if d.squashImageDriver != nil {
		features |= d.squashImageDriver.Features()
	}
	d.overlayfsImageDriver = image.GetDriver("overlayfsfuse")
	if d.overlayfsImageDriver != nil {
		features |= d.overlayfsImageDriver.Features()
	}
	return features
}

func (d *fuseappsDriver) Mount(params *image.MountParams, mfunc image.MountFunc) error {
	if params.Filesystem == "overlay" {
		if d.overlayfsImageDriver != nil {
			return d.overlayfsImageDriver.Mount(params, mfunc)
		}
	} else {
		if d.squashImageDriver != nil {
			return d.squashImageDriver.Mount(params, mfunc)
		}
	}
	return fmt.Errorf("No image driver registered for type %v", params.Filesystem)
}

func (d *fuseappsDriver) Start(params *image.DriverParams) error {
	if d.squashImageDriver != nil {
		err := d.squashImageDriver.Start(params)
		if err != nil {
			return err
		}
	}
	if d.overlayfsImageDriver != nil {
		err := d.overlayfsImageDriver.Start(params)
		if err != nil {
			return err
		}
	}
	return nil
}

func (d *fuseappsDriver) Stop() error {
	if d.squashImageDriver != nil {
		err := d.squashImageDriver.Stop()
		if err != nil {
			return err
		}
	}
	if d.overlayfsImageDriver != nil {
		err := d.overlayfsImageDriver.Stop()
		if err != nil {
			return err
		}
	}
	return nil
}
