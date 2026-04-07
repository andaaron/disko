package sysfs

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buildFakeDriverDir creates a tempdir layout mirroring /sys/bus/pci when a
// RAID PCI driver is loaded:
//
//	<root>/drivers/<driver>/<bdf>  -> <root>/devices/pci0000/<bdf>
//	<root>/drivers/<driver>/module -> <root>/module/<driver>
//
// It returns the driver directory and the resolved device paths that
// GetSysPaths should return for it.
func buildFakeDriverDir(t *testing.T, driver string, bdfs []string) (string, []string) {
	t.Helper()
	root := t.TempDir()

	driverDir := filepath.Join(root, "drivers", driver)
	require.NoError(t, os.MkdirAll(driverDir, 0o755))

	// Real driver dirs also contain a "module" symlink; GetSysPaths must
	// skip it because its name has no ':' (the "*:*" glob excludes it).
	modulePath := filepath.Join(root, "module", driver)
	require.NoError(t, os.MkdirAll(modulePath, 0o755))
	require.NoError(t, os.Symlink(modulePath, filepath.Join(driverDir, "module")))

	resolved := make([]string, 0, len(bdfs))
	for _, bdf := range bdfs {
		dev := filepath.Join(root, "devices", "pci0000", bdf)
		require.NoError(t, os.MkdirAll(dev, 0o755))
		require.NoError(t, os.Symlink(dev, filepath.Join(driverDir, bdf)))

		resolvedDev, err := filepath.EvalSymlinks(dev)
		require.NoError(t, err)
		resolved = append(resolved, resolvedDev)
	}

	return driverDir, resolved
}

func TestGetSysPaths_EmptyDriverDir(t *testing.T) {
	driverDir, _ := buildFakeDriverDir(t, "megaraid_sas", nil)
	assert.Empty(t, GetSysPaths(driverDir))
}

func TestGetSysPaths_SingleBoundDevice(t *testing.T) {
	driverDir, resolved := buildFakeDriverDir(t, "megaraid_sas",
		[]string{"0000:3c:00.0"})

	assert.ElementsMatch(t, resolved, GetSysPaths(driverDir))
}

func TestGetSysPaths_MultipleBoundDevices(t *testing.T) {
	bdfs := []string{"0000:3c:00.0", "0000:82:00.0"}
	driverDir, resolved := buildFakeDriverDir(t, "mpi3mr", bdfs)

	assert.ElementsMatch(t, resolved, GetSysPaths(driverDir))
}

func TestIsSysPathRAID_NoHostSegment(t *testing.T) {
	assert.False(t, IsSysPathRAID("/sys/devices/virtual/block/loop0", ""))
	assert.False(t, IsSysPathRAID("/devices/virtual/block/loop0", ""))
}
