package linux

import (
	"errors"
	"fmt"
	"os"
	"path"
	"testing"

	"github.com/stretchr/testify/assert"
	"machinerun.io/disko"
	"machinerun.io/disko/partid"
)

func TestGetDiskProperties(t *testing.T) {
	azureSys := ("/devices/LNXSYSTM:00/LNXSYBUS:00/PNP0A03:00/device:07/VMBUS:01" +
		"/00000000-0001-8899-0000-000000000000/host1/target1:0:1/1:0:1:0/block/sdb")
	scsiSys := "/devices/pci0000:00/0000:00:02.2/0000:05:00.0/host0/target0:0:8/0:0:8:0/block/sda"

	tables := []struct {
		info     disko.UdevInfo
		expected disko.PropertySet
	}{
		{
			disko.UdevInfo{
				Name:     "sda",
				SysPath:  scsiSys,
				Symlinks: []string{},
				Properties: map[string]string{
					"ID_MODEL":    "SPCC M.2 PCIe SSD",
					"ID_REVISION": "ECFM22.6"}},
			disko.PropertySet{disko.Ephemeral: false}},
		{
			disko.UdevInfo{
				Name:     "sdb",
				SysPath:  azureSys,
				Symlinks: []string{},
				Properties: map[string]string{
					"ID_MODEL":    "SPCC M.2 PCIe SSD",
					"ID_REVISION": "ECFM22.6"}},
			disko.PropertySet{disko.Ephemeral: true}},
		{
			disko.UdevInfo{
				Name:     "sdb",
				SysPath:  azureSys,
				Symlinks: []string{},
				Properties: map[string]string{
					"DM_MULTIPATH_DEVICE_PATH": "0",
					"ID_SERIAL_SHORT":          "AWS628703BD8E5BEB551",
					"ID_WWN":                   "nvme.1d0f-4157...4616e63652053746f72616765-00000001",
					"ID_MODEL":                 "Amazon EC2 NVMe Instance Storage",
					"ID_REVISION":              "0",
					"ID_SERIAL":                "Amazon EC2 NVMe Instance Storage_AWS628703BD8E5BEB551"}},

			disko.PropertySet{disko.Ephemeral: true}},
	}

	for _, table := range tables {
		found := getDiskProperties(table.info)
		bad := []disko.Property{}

		for k, v := range table.expected {
			if found[k] != v {
				bad = append(bad, k)
			}
		}

		for k, v := range found {
			if table.expected[k] != v {
				bad = append(bad, k)
			}
		}

		if len(bad) != 0 {
			t.Errorf("getDiskProperties(%v) returned '%v'. expected '%v'",
				table.info, found, table.expected)
		}
	}
}

func genEmptyDisk(tmpd string, fsize uint64) (disko.Disk, error) {
	fpath := path.Join(tmpd, "mydisk")

	disk := disko.Disk{
		Name:       "mydisk",
		Path:       fpath,
		Size:       fsize,
		SectorSize: sectorSize512,
	}

	if err := os.WriteFile(fpath, []byte{}, 0600); err != nil {
		return disk, fmt.Errorf("Failed to write to a temp file: %s", err)
	}

	if err := os.Truncate(fpath, int64(fsize)); err != nil {
		return disk, fmt.Errorf("Failed create empty file: %s", err)
	}

	fs := disk.FreeSpaces()
	if len(fs) != 1 {
		return disk, fmt.Errorf("Expected 1 free space, found %d", fs)
	}

	return disk, nil
}

func TestCreatePartitionsMBR(t *testing.T) {
	ast := assert.New(t)

	tmpd, err := os.MkdirTemp("", "disko_test")
	if err != nil {
		t.Fatalf("Failed to create tempdir: %s", err)
	}

	defer os.RemoveAll(tmpd)

	disk, err := genEmptyDisk(tmpd, 50*disko.Mebibyte)
	if err != nil {
		t.Fatalf("Creation of temp disk failed: %s", err)
	}

	disk.Table = disko.MBR

	part1 := disko.Partition{
		Start:  4 * disko.Mebibyte,
		Last:   20*disko.Mebibyte - 1,
		Type:   partid.LinuxFS,
		Name:   "ignored-for-mbr",
		ID:     disko.GenGUID(),
		Number: uint(1),
	}

	pSet := disko.PartitionSet{1: part1}

	sys := System()
	if err := sys.CreatePartitions(disk, pSet); err != nil {
		t.Errorf("CreatePartitions failed: %s", err)
	}

	fp, err := os.Open(disk.Path)
	if err != nil {
		t.Fatalf("Failed to open disk image %s: %s", disk.Path, err)
	}

	pSetFound, _, _, err := findPartitions(fp)
	if err != nil {
		t.Fatalf("Failed to findPartitions on %s: %s", disk.Path, err)
	}

	if len(pSetFound) != len(pSet) {
		t.Errorf("Scanned found %d partitions, expected %d", len(pSetFound), len(pSet))
	}

	mbrTypeLinux := disko.PartType{0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x83}
	expPart1 := part1
	expPart1.Type = mbrTypeLinux
	expPart1.Name = ""
	expPart1.ID = disko.GUID{}

	scannedDisk, err := sys.ScanDisk(disk.Path)
	if err != nil {
		t.Errorf("Failed to scan disk-image")
	}

	ast.Equal(expPart1, pSetFound[1])

	ast.Equal(disk.Size, scannedDisk.Size)
	ast.Equal(disko.FILESYSTEM, scannedDisk.Attachment)
	ast.Equal(disko.TYPEFILE, scannedDisk.Type)
	ast.Equal(disko.MBR, scannedDisk.Table)
}

func TestCreatePartitions(t *testing.T) {
	ast := assert.New(t)

	tmpd, err := os.MkdirTemp("", "disko_test")
	if err != nil {
		t.Fatalf("Failed to create tempdir: %s", err)
	}

	defer os.RemoveAll(tmpd)

	disk, err := genEmptyDisk(tmpd, 50*disko.Mebibyte)
	if err != nil {
		t.Fatalf("Creation of temp disk failed: %s", err)
	}

	part1 := disko.Partition{
		Start:  4 * disko.Mebibyte,
		Last:   20*disko.Mebibyte - 1,
		Type:   partid.LinuxHome,
		Name:   "mytest 1",
		ID:     disko.GenGUID(),
		Number: uint(1),
	}

	part2 := disko.Partition{
		Start:  20 * disko.Mebibyte,
		Last:   40*disko.Mebibyte - 1,
		Type:   partid.LinuxFS,
		Name:   "mytest 2",
		ID:     disko.GenGUID(),
		Number: uint(2),
	}

	pSet := disko.PartitionSet{1: part1, 2: part2}

	sys := System()
	if err := sys.CreatePartitions(disk, pSet); err != nil {
		t.Errorf("CreatePartitions failed: %s", err)
	}

	fp, err := os.Open(disk.Path)
	if err != nil {
		t.Fatalf("Failed to open disk image %s: %s", disk.Path, err)
	}

	pSetFound, _, _, err := findPartitions(fp)
	if err != nil {
		t.Fatalf("Failed to findPartitions on %s: %s", disk.Path, err)
	}

	if len(pSetFound) != len(pSet) {
		t.Errorf("Scanned found %d partitions, expected %d", len(pSetFound), len(pSet))
	}

	ast.Equal(part1, pSetFound[1])
	ast.Equal(part2, pSetFound[2])

	scannedDisk, err := sys.ScanDisk(disk.Path)
	if err != nil {
		t.Errorf("Failed to scan disk-image")
	}

	ast.Equal(disk.Size, scannedDisk.Size)
	ast.Equal(disko.FILESYSTEM, scannedDisk.Attachment)
	ast.Equal(disko.TYPEFILE, scannedDisk.Type)
}

type mockRAIDController struct {
	diskType          disko.DiskType
	err               error
	sysfsPath         string
	isSysPathRAID     bool
	getDiskTypeCalled bool
	getDiskTypePath   string
	getDiskTypeUdev   disko.UdevInfo
}

func (m *mockRAIDController) GetDiskType(path string, udInfo disko.UdevInfo) (disko.DiskType, error) {
	m.getDiskTypeCalled = true
	m.getDiskTypePath = path
	m.getDiskTypeUdev = udInfo
	return m.diskType, m.err
}

func (m *mockRAIDController) DriverSysfsPath() string {
	return m.sysfsPath
}

// newTestLinuxSystem builds a linuxSystem whose sysfs lookup is stubbed by
// consulting the isSysPathRAID field on the matching mock (matched by
// DriverSysfsPath), so tests don't need a real /sys mount.
func newTestLinuxSystem(ctrls ...*mockRAIDController) *linuxSystem {
	rc := make([]RAIDController, 0, len(ctrls))
	for _, c := range ctrls {
		rc = append(rc, c)
	}

	return &linuxSystem{
		raidctrls: rc,
		isSysPathRAID: func(_, driverSysPath string) bool {
			for _, c := range ctrls {
				if c.sysfsPath == driverSysPath {
					return c.isSysPathRAID
				}
			}
			return false
		},
	}
}

func TestGetDiskTypeRAIDMatchHDD(t *testing.T) {
	ast := assert.New(t)
	mock := &mockRAIDController{
		diskType:      disko.HDD,
		err:           nil,
		sysfsPath:     "/sys/bus/pci/drivers/megaraid_sas",
		isSysPathRAID: true,
	}

	ls := newTestLinuxSystem(mock)
	udInfo := disko.UdevInfo{Properties: map[string]string{"DEVPATH": "/devices/pci0000:00/0000:00:01.0/0000:01:00.0/host0/target0:2:0/0:2:0:0/block/sda"}}

	dtype, err := ls.GetDiskType("/dev/sda", udInfo)
	ast.NoError(err)
	ast.Equal(disko.HDD, dtype)
	ast.True(mock.getDiskTypeCalled)
	ast.Equal("/dev/sda", mock.getDiskTypePath)
}

func TestGetDiskTypeRAIDMatchSSD(t *testing.T) {
	ast := assert.New(t)
	mock := &mockRAIDController{
		diskType:      disko.SSD,
		err:           nil,
		sysfsPath:     "/sys/bus/pci/drivers/megaraid_sas",
		isSysPathRAID: true,
	}

	ls := newTestLinuxSystem(mock)
	udInfo := disko.UdevInfo{Properties: map[string]string{"DEVPATH": "/devices/pci0000:00/0000:00:01.0/0000:01:00.0/host0/target0:2:0/0:2:0:0/block/sda"}}

	dtype, err := ls.GetDiskType("/dev/sda", udInfo)
	ast.NoError(err)
	ast.Equal(disko.SSD, dtype)
	ast.True(mock.getDiskTypeCalled)
}

func TestGetDiskTypeJBODFallback(t *testing.T) {
	ast := assert.New(t)
	mock := &mockRAIDController{
		diskType:      disko.Unknown,
		err:           disko.ErrDiskTypeUndetermined,
		sysfsPath:     "/sys/bus/pci/drivers/megaraid_sas",
		isSysPathRAID: true,
	}

	ls := newTestLinuxSystem(mock)
	udInfo := disko.UdevInfo{
		Name: "sda",
		Properties: map[string]string{
			"DEVPATH": "/devices/pci0000:00/0000:00:01.0/0000:01:00.0/host0/target0:2:0/0:2:0:0/block/sda",
			"ID_BUS":  "scsi",
			"DEVTYPE": "disk",
		},
	}

	dtype, err := ls.GetDiskType("/dev/sda", udInfo)

	ast.True(mock.getDiskTypeCalled, "RAID controller should have been consulted")
	ast.NoError(err, "ErrDiskTypeUndetermined should not propagate as a fatal error")
	ast.Equal(disko.HDD, dtype, "should fall through to generic detection (HDD default)")
}

func TestGetDiskTypeWrappedSentinelFallback(t *testing.T) {
	ast := assert.New(t)
	wrappedErr := fmt.Errorf("controller 0: %w", disko.ErrDiskTypeUndetermined)

	mock := &mockRAIDController{
		diskType:      disko.Unknown,
		err:           wrappedErr,
		sysfsPath:     "/sys/bus/pci/drivers/megaraid_sas",
		isSysPathRAID: true,
	}

	ls := newTestLinuxSystem(mock)
	udInfo := disko.UdevInfo{Properties: map[string]string{"DEVPATH": "/devices/pci0000:00/0000:00:01.0/0000:01:00.0/host0/target0:2:0/0:2:0:0/block/sda"}}

	_, err := ls.GetDiskType("/dev/sda", udInfo)
	ast.NoError(err, "wrapped ErrDiskTypeUndetermined should still trigger fallback via errors.Is")
}

func TestGetDiskTypeRAIDRealError(t *testing.T) {
	ast := assert.New(t)
	mock := &mockRAIDController{
		diskType:      disko.Unknown,
		err:           fmt.Errorf("storcli binary crashed"),
		sysfsPath:     "/sys/bus/pci/drivers/megaraid_sas",
		isSysPathRAID: true,
	}

	ls := newTestLinuxSystem(mock)
	udInfo := disko.UdevInfo{Properties: map[string]string{"DEVPATH": "/devices/pci0000:00/0000:00:01.0/0000:01:00.0/host0/target0:2:0/0:2:0:0/block/sda"}}

	_, err := ls.GetDiskType("/dev/sda", udInfo)
	ast.Error(err)
	ast.Contains(err.Error(), "failed to get diskType")
	ast.Contains(err.Error(), "storcli binary crashed")
	ast.False(errors.Is(err, disko.ErrDiskTypeUndetermined))
}

func TestGetDiskTypeNoRAIDMatch(t *testing.T) {
	ast := assert.New(t)
	mock := &mockRAIDController{
		diskType:      disko.HDD,
		err:           nil,
		sysfsPath:     "/sys/bus/pci/drivers/megaraid_sas",
		isSysPathRAID: false,
	}

	ls := newTestLinuxSystem(mock)
	udInfo := disko.UdevInfo{Properties: map[string]string{"DEVPATH": "/devices/pci0000:00/0000:00:01.0/0000:01:00.0/host0/target0:2:0/0:2:0:0/block/sda"}}

	_, err := ls.GetDiskType("/dev/sda", udInfo)
	ast.False(mock.getDiskTypeCalled, "should not call GetDiskType when sysfs path doesn't match")
	_ = err
}

func TestGetDiskTypeMultiControllerJBODFallback(t *testing.T) {
	ast := assert.New(t)

	megaraidMock := &mockRAIDController{
		diskType:      disko.Unknown,
		err:           disko.ErrDiskTypeUndetermined,
		sysfsPath:     "/sys/bus/pci/drivers/megaraid_sas",
		isSysPathRAID: true,
	}

	smartpqiMock := &mockRAIDController{
		diskType:      disko.HDD,
		err:           nil,
		sysfsPath:     "/sys/bus/pci/drivers/smartpqi",
		isSysPathRAID: true,
	}

	ls := newTestLinuxSystem(megaraidMock, smartpqiMock)
	udInfo := disko.UdevInfo{Properties: map[string]string{"DEVPATH": "/devices/pci0000:00/0000:00:01.0/0000:01:00.0/host0/target0:2:0/0:2:0:0/block/sda"}}

	_, err := ls.GetDiskType("/dev/sda", udInfo)
	ast.NoError(err)
	ast.True(megaraidMock.getDiskTypeCalled, "megaraid should have been tried")
	ast.False(smartpqiMock.getDiskTypeCalled, "smartpqi should NOT be tried after break from megaraid ErrDiskTypeUndetermined")
}

// udevInfoFallbackStub returns a UdevInfo that drives getDiskType's
// nvme-prefix short-circuit, so fallback tests get a deterministic
// (disko.NVME, nil) result without touching /dev or sysfs. The observed
// disk type is a fingerprint that the fallback ran; it is not itself under
// test.
func udevInfoFallbackStub(devpath string) disko.UdevInfo {
	return disko.UdevInfo{
		Name: "nvme0n1",
		Properties: map[string]string{
			"DEVPATH": devpath,
			"DEVTYPE": "disk",
		},
	}
}

func TestResolveDiskType_RAIDMatchSSD(t *testing.T) {
	ast := assert.New(t)
	mock := &mockRAIDController{
		diskType:      disko.SSD,
		isSysPathRAID: true,
	}

	ls := newTestLinuxSystem(mock)

	dType, onRAID, err := ls.resolveDiskType("/dev/sda",
		disko.UdevInfo{Properties: map[string]string{"DEVPATH": "/devices/pci0000:00/0000:00:01.0/0000:01:00.0/host0/target0:2:0/0:2:0:0/block/sda"}})
	ast.NoError(err)
	ast.True(onRAID)
	ast.Equal(disko.SSD, dType)
	ast.True(mock.getDiskTypeCalled)
	ast.Equal("/dev/sda", mock.getDiskTypePath)
}

func TestResolveDiskType_RAIDMatchHDD(t *testing.T) {
	ast := assert.New(t)
	mock := &mockRAIDController{
		diskType:      disko.HDD,
		isSysPathRAID: true,
	}

	ls := newTestLinuxSystem(mock)

	dType, onRAID, err := ls.resolveDiskType("/dev/sda",
		disko.UdevInfo{Properties: map[string]string{"DEVPATH": "/devices/pci0000:00/0000:00:01.0/0000:01:00.0/host0/target0:2:0/0:2:0:0/block/sda"}})
	ast.NoError(err)
	ast.True(onRAID)
	ast.Equal(disko.HDD, dType)
}

// An ErrDiskTypeUndetermined from the controller must not propagate; resolveDiskType
// falls through to the udev classifier.
func TestResolveDiskType_JBODFallsThroughToUdev(t *testing.T) {
	ast := assert.New(t)
	mock := &mockRAIDController{
		err:           disko.ErrDiskTypeUndetermined,
		isSysPathRAID: true,
	}

	ls := newTestLinuxSystem(mock)

	dType, onRAID, err := ls.resolveDiskType("/dev/nvme0n1",
		udevInfoFallbackStub("/devices/pci0000:00/0000:00:1c.4/0000:04:00.0/nvme/nvme0/nvme0n1"))
	ast.NoError(err)
	ast.False(onRAID)
	ast.True(mock.getDiskTypeCalled)
	ast.Equal(disko.NVME, dType)
}

// Same contract as the previous test, but the sentinel is wrapped via
// fmt.Errorf("...: %w", ...); errors.Is must still trigger the fallback.
func TestResolveDiskType_WrappedJBODFallsThroughToUdev(t *testing.T) {
	ast := assert.New(t)
	mock := &mockRAIDController{
		err:           fmt.Errorf("controller 0: %w", disko.ErrDiskTypeUndetermined),
		isSysPathRAID: true,
	}

	ls := newTestLinuxSystem(mock)

	dType, onRAID, err := ls.resolveDiskType("/dev/nvme0n1",
		udevInfoFallbackStub("/devices/pci0000:00/0000:00:1c.4/0000:04:00.0/nvme/nvme0/nvme0n1"))
	ast.NoError(err)
	ast.False(onRAID)
	ast.Equal(disko.NVME, dType)
}

// Non-sentinel controller errors must surface to the caller, not be swallowed
// into the udev fallback.
func TestResolveDiskType_RealErrorIsPropagated(t *testing.T) {
	ast := assert.New(t)
	mock := &mockRAIDController{
		err:           fmt.Errorf("storcli binary crashed"),
		isSysPathRAID: true,
	}

	ls := newTestLinuxSystem(mock)

	_, onRAID, err := ls.resolveDiskType("/dev/sda",
		disko.UdevInfo{Properties: map[string]string{"DEVPATH": "/devices/pci0000:00/0000:00:01.0/0000:01:00.0/host0/target0:2:0/0:2:0:0/block/sda"}})
	ast.Error(err)
	ast.False(onRAID)
	ast.Contains(err.Error(), "storcli binary crashed")
}

// When no controller claims the devpath via IsSysPathRAID, resolveDiskType
// skips the RAID path entirely and classifies the disk via udev.
func TestResolveDiskType_NoRAIDMatchFallsThroughToUdev(t *testing.T) {
	ast := assert.New(t)
	mock := &mockRAIDController{
		isSysPathRAID: false,
	}

	ls := newTestLinuxSystem(mock)

	dType, onRAID, err := ls.resolveDiskType("/dev/nvme0n1",
		udevInfoFallbackStub("/devices/pci0000:00/0000:00:1c.4/0000:04:00.0/nvme/nvme0/nvme0n1"))
	ast.NoError(err)
	ast.False(onRAID)
	ast.False(mock.getDiskTypeCalled)
	ast.Equal(disko.NVME, dType)
}

// Once a matching controller reports ErrDiskTypeUndetermined, iteration stops (a
// JBOD on one HBA cannot simultaneously be a VD on another) and we fall
// through to the udev classifier exactly once.
func TestResolveDiskType_MultiControllerJBODStopsIteration(t *testing.T) {
	ast := assert.New(t)

	first := &mockRAIDController{
		err:           disko.ErrDiskTypeUndetermined,
		isSysPathRAID: true,
	}
	second := &mockRAIDController{
		diskType:      disko.SSD,
		isSysPathRAID: true,
	}

	ls := newTestLinuxSystem(first, second)

	dType, onRAID, err := ls.resolveDiskType("/dev/nvme0n1",
		udevInfoFallbackStub("/devices/pci0000:00/0000:00:1c.4/0000:04:00.0/nvme/nvme0/nvme0n1"))
	ast.NoError(err)
	ast.False(onRAID)
	ast.True(first.getDiskTypeCalled)
	ast.False(second.getDiskTypeCalled, "second controller should not be consulted after JBOD break")
	ast.Equal(disko.NVME, dType)
}

// A system with no RAID controllers configured still classifies disks via
// udev and never reports onRAID=true.
func TestResolveDiskType_NoControllersConfiguredUsesUdev(t *testing.T) {
	ast := assert.New(t)

	ls := newTestLinuxSystem()

	dType, onRAID, err := ls.resolveDiskType("/dev/nvme0n1",
		udevInfoFallbackStub("/devices/pci0000:00/0000:00:1c.4/0000:04:00.0/nvme/nvme0/nvme0n1"))
	ast.NoError(err)
	ast.False(onRAID)
	ast.Equal(disko.NVME, dType)
}

// disko.Unknown is reserved for error paths; a driver that returns it
// with err == nil has violated the contract. resolveDiskType must catch
// this and surface a hard error rather than let Unknown reach callers.
func TestResolveDiskType_UnknownWithNilErrorIsRejected(t *testing.T) {
	ast := assert.New(t)
	mock := &mockRAIDController{
		diskType:      disko.Unknown,
		err:           nil,
		sysfsPath:     "/sys/bus/pci/drivers/megaraid_sas",
		isSysPathRAID: true,
	}

	ls := newTestLinuxSystem(mock)

	dType, onRAID, err := ls.resolveDiskType("/dev/sda",
		disko.UdevInfo{Properties: map[string]string{"DEVPATH": "/devices/pci0000:00/0000:00:01.0/0000:01:00.0/host0/target0:2:0/0:2:0:0/block/sda"}})
	ast.Error(err)
	ast.Contains(err.Error(), "disko.Unknown")
	ast.Contains(err.Error(), "contract violated")
	ast.False(onRAID)
	ast.Equal(disko.Unknown, dType)
}
