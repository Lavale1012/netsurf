package network

import (
	"errors"
	"os"

	psnet "github.com/shirou/gopsutil/v4/net"
)

// ErrConnectionsUnavailable reports that the system-wide connection table
// could not be read. On macOS that means the process lacks elevated
// privileges. Callers translate this into their own domain: the HTTP layer
// returns 503, a sampler would mark the dashboard state.
var ErrConnectionsUnavailable = errors.New("connections unavailable: needs elevated privileges")

// Addr is one end of a connection. gopsutil's type already carries the
// field names and JSON tags this API emits, so alias it rather than
// maintaining a parallel copy.
type Addr = psnet.Addr

// Connection is a single established connection, shaped to match the
// JSON the Python implementation emitted.
type Connection struct {
	Laddr  *Addr  `json:"laddr"`
	Raddr  *Addr  `json:"raddr"`
	Status string `json:"status"`
	PID    int32  `json:"pid"`
}

// GetConnections returns the current established inet connections.
//
// Only connections with a remote address and ESTABLISHED status are
// included; listening sockets have no remote peer and are filtered out.
func GetConnections() ([]Connection, error) {
	conns, err := psnet.Connections("inet")
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return nil, ErrConnectionsUnavailable
		}
		return nil, err
	}

	// Non-nil empty slice so the JSON is [] rather than null.
	connections := make([]Connection, 0, len(conns))
	for _, c := range conns {
		// Raddr.IP is empty on listening sockets — the Go equivalent of
		// psutil's empty raddr tuple.
		if c.Raddr.IP == "" || c.Status != "ESTABLISHED" {
			continue
		}

		conn := Connection{
			Raddr:  &c.Raddr,
			Status: c.Status,
			PID:    c.Pid,
		}
		if c.Laddr.IP != "" {
			conn.Laddr = &c.Laddr
		}
		connections = append(connections, conn)
	}

	return connections, nil
}
