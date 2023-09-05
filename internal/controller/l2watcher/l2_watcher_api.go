package l2watcher

import (
	"sync/atomic"

	"chain-monitor/internal/controller"
)

// StartNumber retrieves the current starting block number
// that the L2Watcher is tracking.
func (l2 *L2Watcher) StartNumber() uint64 {
	return atomic.LoadUint64(&l2.startNumber)
}

func (l2 *L2Watcher) setStartNumber(number uint64) {
	atomic.StoreUint64(&l2.startNumber, number)
}

// SafeNumber retrieves the current safe block number
// up to which it's considered secure to watch.
func (l2 *L2Watcher) SafeNumber() uint64 {
	return atomic.LoadUint64(&l2.safeNumber)
}

func (l2 *L2Watcher) setSafeNumber(number uint64) {
	atomic.StoreUint64(&l2.safeNumber, number)
}

// IsReady checks whether the L2Watcher is ready. It's considered ready
// when the starting block number matches the safe block number.
func (l2 *L2Watcher) IsReady() bool {
	return l2.StartNumber() == l2.SafeNumber()
}

// SetMonitor sets the monitoring API for the L2Watcher.
func (l2 *L2Watcher) SetMonitor(monitor controller.MonitorAPI) {
	l2.filter.setMonitorAPI(monitor)
}
