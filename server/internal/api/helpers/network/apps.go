package network

// AppRollup groups connections and traffic by owning process.
type AppRollup struct {
	App         string `json:"app"`
	PID         int32  `json:"pid"`
	Connections int    `json:"connections"`
	BytesSent   uint64 `json:"bytesSent"`
	BytesRecv   uint64 `json:"bytesRecv"`
}

// CollectApps returns per-application rollups of the current connections.
//
// TODO: implement. Group the connection list by owning process, resolving
// each PID to an application name.
//
// Guard every process lookup. Processes routinely exit between the moment
// the connection table is read and the moment the PID is resolved — that is
// a normal race, not an exceptional case. A connection whose process has
// vanished should still be reported, with the app name left unknown, rather
// than dropped or failing the whole request.
func CollectApps() ([]AppRollup, error) {
	// Non-nil so the JSON is [] rather than null.
	return []AppRollup{}, nil
}
