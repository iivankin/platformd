//go:build !linux

package cgroupstats

type memoryPeakReader struct{}

func newMemoryPeakReader() *memoryPeakReader {
	return &memoryPeakReader{}
}

func (*memoryPeakReader) read(_ string, _ string, current uint64) uint64 {
	return current
}

func (*memoryPeakReader) forget(string) {}

func (*memoryPeakReader) close() error { return nil }
