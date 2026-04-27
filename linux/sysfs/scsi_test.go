package sysfs

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeBlockDeviceSymlink wires up a fake sysfs entry at
// <root>/block/<kname>/device pointing at
// ../../scsi_device/<host:controller:target:lun>.
func makeBlockDeviceSymlink(t *testing.T, root, kname, hctl string) {
	t.Helper()

	blockDir := filepath.Join(root, "block", kname)
	require.NoError(t, os.MkdirAll(blockDir, 0o755), "mkdir %q", blockDir)

	scsiDir := filepath.Join(root, "scsi_device", hctl)
	require.NoError(t, os.MkdirAll(scsiDir, 0o755), "mkdir %q", scsiDir)

	link := filepath.Join(blockDir, "device")
	target := filepath.Join("..", "..", "scsi_device", hctl)
	require.NoError(t, os.Symlink(target, link), "symlink")
}

func TestReadSCSITargetJBOD(t *testing.T) {
	root := t.TempDir()
	makeBlockDeviceSymlink(t, root, "sdb", "0:2:3:0")

	target, ok, err := ReadSCSITarget(root, "sdb")
	require.NoError(t, err)
	require.True(t, ok, "expected ok=true for a SCSI-backed block device")
	assert.Equal(t, 3, target, "target")
}

// An NVMe/virtio-style block device has no Host:Controller:Target:LUN
// "device" symlink.
// ReadSCSITarget must report ok=false (not an error) so the caller can
// fall through to generic udev detection.
func TestReadSCSITargetNoDevice(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "block", "nvme0n1"), 0o755))

	_, ok, err := ReadSCSITarget(root, "nvme0n1")
	require.NoError(t, err)
	assert.False(t, ok, "expected ok=false when device symlink is absent")
}

// A "device" symlink whose last segment isn't Host:Controller:Target:LUN
// (e.g. points at a PCI node) is malformed for a SCSI block device and
// should be reported as an error so the caller can log/diagnose. Callers
// are expected to filter non-SCSI devices upstream via udev (ID_SCSI=1).
func TestReadSCSITargetNonHCTL(t *testing.T) {
	root := t.TempDir()
	blockDir := filepath.Join(root, "block", "vda")
	require.NoError(t, os.MkdirAll(blockDir, 0o755))
	other := filepath.Join(root, "devices", "virtio0")
	require.NoError(t, os.MkdirAll(other, 0o755))
	require.NoError(t, os.Symlink(filepath.Join("..", "..", "devices", "virtio0"),
		filepath.Join(blockDir, "device")))

	_, ok, err := ReadSCSITarget(root, "vda")
	require.Error(t, err, "expected error for malformed Host:Controller:Target:LUN link target")
	assert.False(t, ok, "expected ok=false when link target is not Host:Controller:Target:LUN")
}

func TestReadSCSITargetBadTarget(t *testing.T) {
	root := t.TempDir()
	makeBlockDeviceSymlink(t, root, "sdc", "0:2:notanum:0")

	_, ok, err := ReadSCSITarget(root, "sdc")
	require.Error(t, err, "expected parse error")
	assert.False(t, ok, "expected ok=false on parse error")
}

func TestReadSCSITargetEmptyKname(t *testing.T) {
	_, ok, err := ReadSCSITarget("/sys", "")
	require.NoError(t, err)
	assert.False(t, ok, "expected ok=false for empty kname")
}
