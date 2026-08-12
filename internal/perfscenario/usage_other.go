//go:build !darwin && !linux

package perfscenario

import "errors"

type processUsage struct {
	cpuNS        int64
	peakRSSBytes int64
}

func readProcessUsage() (processUsage, error) {
	return processUsage{}, errors.New("process usage is unavailable on this platform")
}
