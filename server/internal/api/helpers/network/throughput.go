package network

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

// CollectThroughput returns the current up/down rate.
//
// TODO: implement. Retain the previous net.IOCounters reading plus its
// timestamp, and compute the delta over the measured elapsed time. IO
// counters are cumulative since boot, so a single reading carries no rate
// information at all — throughput is always a difference, never a reading.
func CollectThroughput() (Throughput, error) {
	return Throughput{}, nil
}
