//go:build darwin || linux

package perfscenario

import (
	"fmt"
	"runtime"
	"syscall"
)

type processUsage struct {
	cpuNS        int64
	peakRSSBytes int64
}

func readProcessUsage() (processUsage, error) {
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		return processUsage{}, fmt.Errorf("read process usage: %w", err)
	}
	rss := usage.Maxrss
	if runtime.GOOS == "linux" {
		rss *= 1024
	}
	return processUsage{
		cpuNS:        timevalNS(usage.Utime) + timevalNS(usage.Stime),
		peakRSSBytes: rss,
	}, nil
}

func timevalNS(value syscall.Timeval) int64 {
	return value.Sec*1_000_000_000 + int64(value.Usec)*1_000
}
