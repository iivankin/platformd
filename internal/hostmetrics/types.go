package hostmetrics

type Sample struct {
	ObservedAtMillis int64
	CPUUnits         uint64
	CPUIdleUnits     uint64
	CPUCores         int
	MemoryUsedBytes  uint64
	MemoryTotalBytes uint64
	NetworkRXBytes   uint64
	NetworkTXBytes   uint64
	NetworkInterface string
}

type Reader interface {
	Read() (Sample, error)
}
