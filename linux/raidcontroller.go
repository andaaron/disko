package linux

import "machinerun.io/disko"

type RAIDControllerType string

const (
	MegaRAIDControllerType RAIDControllerType = "megaraid"
	SmartPqiControllerType RAIDControllerType = "smartpqi"
	MPI3MRControllerType   RAIDControllerType = "mpi3mr"
)

type RAIDController interface {
	// Type() RAIDControllerType
	GetDiskType(path string, udInfo disko.UdevInfo) (disko.DiskType, error)
	DriverSysfsPath() string
}
