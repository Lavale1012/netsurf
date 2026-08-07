package network

// Hostname is one resolved remote endpoint.
type Hostname struct {
	IP   string `json:"ip"`
	Host string `json:"host"` // "" while unresolved, or if resolution failed
}

// CollectHostnames returns the reverse-DNS names known so far, from cache.
//
// TODO: implement. Resolution must run off the sampler. Reverse lookups
// block, and the sampler is a fixed-interval loop shared by every connected
// client — one slow lookup stalls the entire stream, not just one frame.
//
// So: resolve out of band, cache by IP, and cache negative results too.
// Unresolvable addresses are common and retrying them every tick is the
// expensive path. Emit frames with raw IPs immediately and let hostnames
// arrive in a later frame.
func CollectHostnames() ([]Hostname, error) {
	// Non-nil so the JSON is [] rather than null.
	return []Hostname{}, nil
}
