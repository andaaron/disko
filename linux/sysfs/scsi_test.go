package sysfs

import (
	"os"
	"path/filepath"
	"testing"
)

// makeBlockDeviceSymlink wires up a fake sysfs entry at
// <root>/block/<kname>/device pointing at ../../scsi_device/<hctl>.
func makeBlockDeviceSymlink(t *testing.T, root, kname, hctl string) {
	t.Helper()

	blockDir := filepath.Join(root, "block", kname)
	if err := os.MkdirAll(blockDir, 0o755); err != nil {
		t.Fatalf("mkdir %q: %s", blockDir, err)
	}

	scsiDir := filepath.Join(root, "scsi_device", hctl)
	if err := os.MkdirAll(scsiDir, 0o755); err != nil {
		t.Fatalf("mkdir %q: %s", scsiDir, err)
	}

	link := filepath.Join(blockDir, "device")
	target := filepath.Join("..", "..", "scsi_device", hctl)
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %s", err)
	}
}

func TestReadSCSITargetJBOD(t *testing.T) {
	root := t.TempDir()
	makeBlockDeviceSymlink(t, root, "sdb", "0:2:3:0")

	target, ok, err := ReadSCSITarget(root, "sdb")
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if !ok {
		t.Fatalf("expected ok=true for a SCSI-backed block device")
	}
	if target != 3 {
		t.Errorf("target: got %d, want 3", target)
	}
}

// An NVMe/virtio-style block device has no H:C:T:L "device" symlink.
// ReadSCSITarget must report ok=false (not an error) so the caller can
// fall through to generic udev detection.
func TestReadSCSITargetNoDevice(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "block", "nvme0n1"), 0o755); err != nil {
		t.Fatalf("mkdir: %s", err)
	}

	_, ok, err := ReadSCSITarget(root, "nvme0n1")
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if ok {
		t.Errorf("expected ok=false when device symlink is absent")
	}
}

// A symlink whose last segment isn't H:C:T:L (e.g. points at a PCI node) must
// not be mistaken for a SCSI device. ReadSCSITarget returns ok=false with no
// error so the caller falls through to udev.
func TestReadSCSITargetNonHCTL(t *testing.T) {
	root := t.TempDir()
	blockDir := filepath.Join(root, "block", "vda")
	if err := os.MkdirAll(blockDir, 0o755); err != nil {
		t.Fatalf("mkdir: %s", err)
	}
	other := filepath.Join(root, "devices", "virtio0")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatalf("mkdir: %s", err)
	}
	if err := os.Symlink(filepath.Join("..", "..", "devices", "virtio0"),
		filepath.Join(blockDir, "device")); err != nil {
		t.Fatalf("symlink: %s", err)
	}

	_, ok, err := ReadSCSITarget(root, "vda")
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if ok {
		t.Errorf("expected ok=false when link target is not H:C:T:L")
	}
}

func TestReadSCSITargetBadTarget(t *testing.T) {
	root := t.TempDir()
	makeBlockDeviceSymlink(t, root, "sdc", "0:2:notanum:0")

	_, ok, err := ReadSCSITarget(root, "sdc")
	if err == nil {
		t.Fatalf("expected parse error, got nil")
	}
	if ok {
		t.Errorf("expected ok=false on parse error")
	}
}

func TestReadSCSITargetEmptyKname(t *testing.T) {
	_, ok, err := ReadSCSITarget("/sys", "")
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if ok {
		t.Errorf("expected ok=false for empty kname")
	}
}
