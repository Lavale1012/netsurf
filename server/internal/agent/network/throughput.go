package network

import (
	"errors"
	"sync"
	"time"

	psnet "github.com/shirou/gopsutil/v4/net"
)

// Throughput is the current transfer rate, derived by diffing two counter
// reads over the elapsed time between them.
type Throughput struct {
	BytesSentPerSec float64 `json:"bytesSentPerSec"`
	BytesRecvPerSec float64 `json:"bytesRecvPerSec"`

	// ElapsedMS is the *measured* interval the rate was computed over, not
	// the nominal sample interval. The sampler loop drifts under load, and
	// dividing by the nominal value silently skews every rate.
	ElapsedMS int64 `json:"elapsedMs"`
}

// reading is one counter sample plus when it was taken.
type reading struct {
	sent, recv uint64
	at         time.Time
}

var (
	tpMu sync.Mutex
	prev *reading // nil until the first call
)

// CollectThroughput returns the current up/down rate.
//
// IO counters are cumulative since boot, so a single reading carries no
// rate information — throughput is always a difference between two reads.
// The first call therefore has nothing to compare against and returns
// zeros; every call after that reports the rate since the previous one.
func CollectThroughput() (Throughput, error) {
	// false = summed across all interfaces, so we get one total rather than
	// per-NIC rows.
	counters, err := psnet.IOCounters(false)
	if err != nil {
		return Throughput{}, err
	}
	if len(counters) == 0 {
		return Throughput{}, errors.New("no network io counters available")
	}

	now := reading{
		sent: counters[0].BytesSent,
		recv: counters[0].BytesRecv,
		at:   time.Now(),
	}

	tpMu.Lock()
	defer tpMu.Unlock()

	last := prev
	prev = &now

	// First call: nothing to diff against yet.
	if last == nil {
		return Throughput{}, nil
	}

	elapsed := now.at.Sub(last.at).Seconds()
	if elapsed <= 0 {
		return Throughput{}, nil
	}

	return Throughput{
		BytesSentPerSec: rate(now.sent, last.sent, elapsed),
		BytesRecvPerSec: rate(now.recv, last.recv, elapsed),
		ElapsedMS:       now.at.Sub(last.at).Milliseconds(),
	}, nil
}

// rate divides the counter delta by elapsed seconds.
//
// The cur < last guard matters: these are uint64, so a counter that went
// backwards — an interface bouncing, or the machine rebooting — would
// underflow to a value near 1.8e19 instead of a small negative. Report 0
// for that tick rather than a nonsense spike.
func rate(cur, last uint64, elapsed float64) float64 {
	if cur < last {
		return 0
	}
	return float64(cur-last) / elapsed
}
