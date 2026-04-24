package megaraid

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// readSCSITarget returns T from /sys/block/<kname>/device -> H:C:T:L.
// On megaraid_sas JBODs, T equals storcli's Drive.DID.
//
// ok=false (nil err) for non-SCSI devices (no "device" symlink, link does
// not end in H:C:T:L, e.g. NVMe, virtio). sysRoot is injectable for tests;
// production passes "/sys".
func readSCSITarget(sysRoot, kname string) (target int, ok bool, err error) {
	if kname == "" {
		return 0, false, nil
	}

	link := filepath.Join(sysRoot, "block", kname, "device")

	dest, err := os.Readlink(link)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("readlink %q: %w", link, err)
	}

	last := filepath.Base(dest)
	parts := strings.Split(last, ":")

	const hctlFields = 4
	if len(parts) != hctlFields {
		return 0, false, nil
	}

	t, cerr := strconv.Atoi(parts[2])
	if cerr != nil {
		return 0, false, fmt.Errorf("parse SCSI target from %q: %w", last, cerr)
	}

	return t, true, nil
}
