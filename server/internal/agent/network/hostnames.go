package network

import (
	"context"
	"net"
	"strings"
	"sync"
	"time"
)

// Hostname is one resolved remote endpoint.
type Hostname struct {
	IP   string `json:"ip"`
	Host string `json:"host"` // "" while unresolved, or if resolution failed
}

const (
	// Successful lookups are cached far longer than failures. Most remote
	// IPs simply have no PTR record, so failures are the common case and
	// re-querying them often is the expensive path.
	hostTTL = 30 * time.Minute
	failTTL = 10 * time.Minute

	lookupTimeout = 2 * time.Second
	queueSize     = 256
	maxHosts      = 4096
)

type hostEntry struct {
	host      string
	expiresAt time.Time
}

var (
	hostMu    sync.Mutex
	hostCache = make(map[string]hostEntry)
	inFlight  = make(map[string]struct{}) // dedupes concurrent lookups for one IP

	lookupQ    = make(chan string, queueSize)
	resolverGo sync.Once
)

// HostFor returns the cached name for ip, or "" when it is unknown or
// unresolvable.
//
// It never blocks. An unknown IP is queued for background resolution and
// the caller gets "" for now — the name appears in a later frame. This is
// the whole point: a reverse lookup can take seconds, and the sampler is a
// fixed-interval loop shared by every connected client, so blocking here
// would stall the entire stream rather than one frame.
func HostFor(ip string) string {
	if ip == "" {
		return ""
	}
	startResolver()

	hostMu.Lock()
	entry, cached := hostCache[ip]
	_, busy := inFlight[ip]
	hostMu.Unlock()

	if cached && time.Now().Before(entry.expiresAt) {
		return entry.host
	}
	if !busy {
		enqueue(ip)
	}
	// A stale entry still beats nothing while the refresh is in flight.
	return entry.host
}

// CollectHostnames returns the names known so far for the current remote
// endpoints, and queues anything still unresolved.
func CollectHostnames() ([]Hostname, error) {
	conns, err := CollectConnections()
	if err != nil {
		return nil, err
	}

	seen := make(map[string]struct{}, len(conns))
	// Non-nil so the JSON is [] rather than null.
	out := make([]Hostname, 0, len(conns))
	for _, c := range conns {
		if c.Raddr == nil || c.Raddr.IP == "" {
			continue
		}
		if _, dup := seen[c.Raddr.IP]; dup {
			continue
		}
		seen[c.Raddr.IP] = struct{}{}
		out = append(out, Hostname{IP: c.Raddr.IP, Host: HostFor(c.Raddr.IP)})
	}
	return out, nil
}

// enqueue submits an IP for background resolution, dropping it if the queue
// is full — a dropped lookup just means the name arrives a tick later.
func enqueue(ip string) {
	hostMu.Lock()
	if len(hostCache) >= maxHosts {
		hostMu.Unlock()
		return
	}
	inFlight[ip] = struct{}{}
	hostMu.Unlock()

	select {
	case lookupQ <- ip:
	default:
		hostMu.Lock()
		delete(inFlight, ip)
		hostMu.Unlock()
	}
}

// startResolver launches the background worker on first use.
func startResolver() {
	resolverGo.Do(func() {
		go func() {
			for ip := range lookupQ {
				resolve(ip)
			}
		}()
	})
}

// resolve performs one lookup and caches the result — including failures,
// which is what stops unresolvable addresses being retried every tick.
func resolve(ip string) {
	ctx, cancel := context.WithTimeout(context.Background(), lookupTimeout)
	defer cancel()

	host := ""
	ttl := failTTL
	if names, err := net.DefaultResolver.LookupAddr(ctx, ip); err == nil && len(names) > 0 {
		// LookupAddr returns FQDNs with a trailing dot.
		host = strings.TrimSuffix(names[0], ".")
		ttl = hostTTL
	}

	hostMu.Lock()
	hostCache[ip] = hostEntry{host: host, expiresAt: time.Now().Add(ttl)}
	delete(inFlight, ip)
	hostMu.Unlock()
}
