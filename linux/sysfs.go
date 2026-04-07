package linux

import (
	"fmt"
	"path/filepath"
	"strings"
)

// IsSysPathRAID checks whether syspath (udevadm DEVPATH) belongs to a RAID
// controller whose PCI driver is registered at driverSysPath.
//
//	syspath will look something like
//	   /devices/pci0000:3a/0000:3a:02.0/0000:3c:00.0/host0/target0:2:2/0:2:2:0/block/sdc
func IsSysPathRAID(syspath string, driverSysPath string) bool {
	if !strings.HasPrefix(syspath, "/sys") {
		syspath = "/sys" + syspath
	}

	if !strings.Contains(syspath, "/host") {
		return false
	}

	fp, err := filepath.EvalSymlinks(syspath)
	if err != nil {
		fmt.Printf("seriously? %s\n", err)
		return false
	}

	for _, path := range GetSysPaths(driverSysPath) {
		if strings.HasPrefix(fp, path) {
			return true
		}
	}

	return false
}

// GetSysPaths returns the resolved PCI device paths for a RAID driver.
func GetSysPaths(driverSysPath string) []string {
	paths := []string{}
	// a raid driver has directory entries for each of the scsi hosts on that controller.
	//   $cd /sys/bus/pci/drivers/<driver name>
	//   $ for d in *; do [ -d "$d" ] || continue; echo "$d -> $( cd "$d" && pwd -P )"; done
	//    0000:3c:00.0 -> /sys/devices/pci0000:3a/0000:3a:02.0/0000:3c:00.0
	//    module -> /sys/module/<driver module name>

	// We take a hack path and consider anything with a ":" in that dir as a host path.
	matches, err := filepath.Glob(driverSysPath + "/*:*")

	if err != nil {
		fmt.Printf("errors: %s\n", err)
		return paths
	}

	for _, p := range matches {
		if fp, err := filepath.EvalSymlinks(p); err == nil {
			paths = append(paths, fp)
		}
	}

	return paths
}
