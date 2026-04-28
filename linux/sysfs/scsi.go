package sysfs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ReadSCSITarget returns Target from /sys/block/<kname>/device ->
// Host:Channel:Target:LUN (H:C:T:L). For SCSI-attached JBOD/passthrough disks
// behind a RAID HBA, Target matches the controller-reported drive ID
// (megaraid Drive.DID, mpi3mr PhysicalDrive.PID), which lets callers
// correlate a Linux block device with an entry in the controller's PD list.
//
// The "device" entry under a SCSI block device is a symlink into
// /sys/class/scsi_device/, e.g.:
//
//	% ls -al /sys/block/sda/device
//	lrwxrwxrwx 1 root root 0 Jan 31 16:11 /sys/block/sda/device -> ../../../2:0:0:0
//
// Callers must only invoke ReadSCSITarget for devices that udev reports as
// SCSI (ID_SCSI=1); virtio-blk, NVMe, ATA/SATA, etc. do not expose this
// symlink and should be filtered upstream. An empty kname is a caller bug
// and is rejected with an error; ok=false with a nil error is reserved for
// the benign missing-"device"-symlink case; a malformed
// Host:Channel:Target:LUN link target is reported as an error. sysRoot is
// injectable for tests; production passes "/sys".
func ReadSCSITarget(sysRoot, kname string) (target int, ok bool, err error) {
	if kname == "" {
		return 0, false, fmt.Errorf("invalid empty kname parameter")
	}

	link := filepath.Join(sysRoot, "block", kname, "device")

	dest, err := os.Readlink(link)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("readlink %q: %w", link, err)
	}

	// Parse out the SCSI device id from the symlink. e.g.
	//   ../../../0:3:110:0  ->  0:3:110:0
	// matching an entry under /sys/class/scsi_device/.
	scsiDeviceID := filepath.Base(dest)
	hctlFields := strings.Split(scsiDeviceID, ":")

	const requiredHCTLFields = 4
	if len(hctlFields) != requiredHCTLFields {
		return 0, false, fmt.Errorf(
			"invalid SCSI Host:Channel:Target:LUN value %q from %q: expected %d fields, got %d",
			scsiDeviceID, link, requiredHCTLFields, len(hctlFields))
	}

	t, cerr := strconv.Atoi(hctlFields[2])
	if cerr != nil {
		return 0, false, fmt.Errorf("parse SCSI target from %q: %w", scsiDeviceID, cerr)
	}

	return t, true, nil
}
