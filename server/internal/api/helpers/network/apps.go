package network

import (
	"cmp"
	"slices"
)

// AppRollup groups connections by owning process.
type AppRollup struct {
	App         string `json:"app"`
	PID         int32  `json:"pid"`
	Connections int    `json:"connections"`

	// BytesSent/BytesRecv are always 0 for now. The connection table does
	// not carry per-connection byte counts — filling these means joining
	// against the captured flows by 4-tuple, which is a separate step.
	BytesSent uint64 `json:"bytesSent"`
	BytesRecv uint64 `json:"bytesRecv"`
}

// CollectApps groups the current connections by owning process.
//
// Connections whose process cannot be identified are still counted, under
// an empty App name — a vanished process is a normal race, not a reason to
// drop the connection or fail the request.
func CollectApps() ([]AppRollup, error) {
	conns, err := CollectConnections()
	if err != nil {
		return nil, err
	}

	byPID := make(map[int32]*AppRollup, len(conns))
	for _, c := range conns {
		r, ok := byPID[c.PID]
		if !ok {
			r = &AppRollup{App: NameForPID(c.PID), PID: c.PID}
			byPID[c.PID] = r
		}
		r.Connections++
	}

	// Non-nil so the JSON is [] rather than null.
	out := make([]AppRollup, 0, len(byPID))
	for _, r := range byPID {
		out = append(out, *r)
	}

	// Busiest first, with a deterministic tiebreak so the order is stable
	// across ticks and the dashboard rows do not jump around.
	slices.SortFunc(out, func(a, b AppRollup) int {
		if c := cmp.Compare(b.Connections, a.Connections); c != 0 {
			return c
		}
		return cmp.Compare(a.PID, b.PID)
	})
	return out, nil
}
