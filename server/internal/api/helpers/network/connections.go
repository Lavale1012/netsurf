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

// Addr is one end of a connection.
type Addr struct {
	IP   string `json:"ip"`
	Port uint32 `json:"port"`
}

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
		if isPermissionErr(err) {
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
			Raddr:  &Addr{IP: c.Raddr.IP, Port: c.Raddr.Port},
			Status: c.Status,
			PID:    c.Pid,
		}
		if c.Laddr.IP != "" {
			conn.Laddr = &Addr{IP: c.Laddr.IP, Port: c.Laddr.Port}
		}
		connections = append(connections, conn)
	}

	return connections, nil
}

func isPermissionErr(err error) bool {
	return errors.Is(err, os.ErrPermission)
}
