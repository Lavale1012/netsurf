package network

import (
	"cmp"
	"errors"
	"fmt"
	"log"
	"net"
	"net/netip"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/gopacket/gopacket/pcap"
)

// ErrCaptureUnavailable reports that live capture is not running. On macOS
// that means /dev/bpf* could not be opened — capture needs elevated
// privileges, unlike the connection table, which does not.
var ErrCaptureUnavailable = errors.New("packet capture unavailable: needs elevated privileges (run with sudo)")

const (
	// maxFlows bounds the accumulator between ticks. Once full, packets for
	// new conversations are counted as overflow rather than growing the map
	// without limit.
	maxFlows = 4096

	// maxEmit bounds what actually goes on the wire, after sorting by bytes.
	// This multiplies with the hub's 300-frame history, so raising it is not
	// free: at ~130 bytes per row, 100 rows is roughly 4MB retained.
	maxEmit = 100

	// snapLen must be large enough to reach the SNI extension in a TLS
	// ClientHello, which commonly runs past 512 bytes.
	snapLen = 1600
)

// Flow is one direction of one conversation, aggregated over a single
// sampling interval. It is a delta, not a running total: the counters reset
// each time the stream drains, so consumers add consecutive frames rather
// than diffing them.
type Flow struct {
	Src     string `json:"src"`
	Dst     string `json:"dst"`
	Proto   string `json:"proto"`
	Packets uint64 `json:"packets"`
	Bytes   uint64 `json:"bytes"`
	Dir     string `json:"dir"`           // "in", "out", or "local"
	SNI     string `json:"sni,omitempty"` // TLS server name, when seen
}

// flowKey identifies one unidirectional conversation. netip.Addr is
// comparable and so usable as a map key; net.IP is a slice and is not.
type flowKey struct {
	src, dst     netip.Addr
	sport, dport uint16
	proto        string
}

type flowStat struct {
	packets uint64
	bytes   uint64
	sni     string
}

var (
	mu       sync.Mutex
	flows    = make(map[flowKey]*flowStat)
	overflow uint64

	// localIPs is swapped wholesale by a refresher goroutine, so readers
	// never see a partially built map.
	localIPs atomic.Pointer[map[netip.Addr]struct{}]

	startErr error // set once by StartCapture; returned by every drain

	// captureDev is the interface StartCapture opened. One handle is shared
	// by every client, so a request asking for a different device cannot be
	// served — the WebSocket handler rejects it rather than silently
	// streaming the wrong interface.
	captureDev string
)

// StartCapture opens the device and begins capturing in the background. Call
// it once at startup. It does not block — capture runs in its own goroutine.
//
// The returned error is also retained, so GetLivePackets keeps reporting the
// same failure rather than looking merely idle.
func StartCapture(device string) error {
	captureDev = device

	addrs, err := localAddrs()
	if err != nil {
		startErr = fmt.Errorf("%w: %v", ErrCaptureUnavailable, err)
		return startErr
	}
	localIPs.Store(&addrs)

	handle, err := pcap.OpenLive(device, snapLen, true, pcap.BlockForever)
	if err != nil {
		startErr = fmt.Errorf("%w: %v", ErrCaptureUnavailable, err)
		return startErr
	}

	// Drop broadcast and multicast in-kernel. On a home network that is most
	// packets by count — mDNS, SSDP, ARP, neighbour discovery — and none of
	// it is this machine talking to the internet. Filtering here is far
	// cheaper than filtering after the copy into Go.
	if err := handle.SetBPFFilter("(ip or ip6) and not broadcast and not multicast"); err != nil {
		handle.Close()
		startErr = fmt.Errorf("%w: bpf filter: %v", ErrCaptureUnavailable, err)
		return startErr
	}

	src := gopacket.NewPacketSource(handle, handle.LinkType())
	// Required for the TLS layer to be reached through the TCP payload;
	// without it decoding stops at LayerTypePayload and SNI is never seen.
	src.DecodeStreamsAsDatagrams = true

	go func() {
		defer handle.Close()
		for pkt := range src.Packets() {
			record(pkt)
		}
	}()

	// Addresses change when a VPN comes up or DHCP renews. Without this,
	// every packet would start classifying as "not ours" and the stream
	// would silently empty.
	go func() {
		for range time.Tick(30 * time.Second) {
			if a, err := localAddrs(); err == nil {
				localIPs.Store(&a)
			}
		}
	}()

	log.Printf("network: capturing on %s", device)
	return nil
}

// GetLivePackets returns the flows observed since the previous call and
// clears the accumulator.
//
// It returns promptly — it only drains what the capture goroutine has already
// folded together. The sampler's loop is shared by every connected client, so
// this must never wait on capture.
func GetLivePackets() ([]Flow, error) {
	if startErr != nil {
		return nil, startErr
	}

	// Swap the map out under the lock and format outside it, so the capture
	// goroutine stalls for a pointer assignment rather than a full walk.
	mu.Lock()
	old := flows
	flows = make(map[flowKey]*flowStat, len(old))
	dropped := overflow
	overflow = 0
	mu.Unlock()

	local := localIPs.Load()
	out := make([]Flow, 0, len(old)) // non-nil so the JSON is [] not null
	for k, st := range old {
		out = append(out, Flow{
			Src:     endpoint(k.src, k.sport),
			Dst:     endpoint(k.dst, k.dport),
			Proto:   k.proto,
			Packets: st.packets,
			Bytes:   st.bytes,
			Dir:     direction(local, k.src, k.dst),
			SNI:     st.sni,
		})
	}

	// Biggest talkers first, with a deterministic tiebreak so the order is
	// stable across ticks.
	slices.SortFunc(out, func(a, b Flow) int {
		if c := cmp.Compare(b.Bytes, a.Bytes); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Src, b.Src); c != 0 {
			return c
		}
		return cmp.Compare(a.Dst, b.Dst)
	})
	if len(out) > maxEmit {
		out = out[:maxEmit]
	}

	if dropped > 0 {
		log.Printf("network: %d packets dropped (flow table full at %d)", dropped, maxFlows)
	}
	return out, nil
}

// record folds one packet into the accumulator. Everything expensive —
// decoding, address parsing, SNI extraction — happens before the lock is
// taken; the critical section is a map lookup and two adds.
func record(pkt gopacket.Packet) {
	var k flowKey
	var netProto uint8

	switch nl := pkt.NetworkLayer().(type) {
	case *layers.IPv4:
		k.src, _ = netip.AddrFromSlice(nl.SrcIP)
		k.dst, _ = netip.AddrFromSlice(nl.DstIP)
		netProto = uint8(nl.Protocol)
	case *layers.IPv6:
		k.src, _ = netip.AddrFromSlice(nl.SrcIP)
		k.dst, _ = netip.AddrFromSlice(nl.DstIP)
		netProto = uint8(nl.NextHeader)
	default:
		return // not an IP packet — nothing to attribute
	}
	k.src, k.dst = k.src.Unmap(), k.dst.Unmap()

	// Take the protocol from the transport layer where there is one. IPv6
	// NextHeader reports the first extension header (0 or 44) rather than the
	// transport protocol, so trusting it would label real TCP traffic "0".
	switch tl := pkt.TransportLayer().(type) {
	case *layers.TCP:
		// Cast to uint16: TCPPort.String() renders "443(https)".
		k.sport, k.dport = uint16(tl.SrcPort), uint16(tl.DstPort)
		k.proto = "tcp"
	case *layers.UDP:
		k.sport, k.dport = uint16(tl.SrcPort), uint16(tl.DstPort)
		k.proto = "udp"
	default:
		k.proto = protoName(netProto)
	}

	var n uint64
	if md := pkt.Metadata(); md != nil && md.Length > 0 {
		n = uint64(md.Length) // true wire size, not the snaplen-truncated copy
	} else {
		n = uint64(len(pkt.Data()))
	}
	sni := extractSNI(pkt)

	mu.Lock()
	defer mu.Unlock()
	st := flows[k]
	if st == nil {
		if len(flows) >= maxFlows {
			overflow++
			return
		}
		st = &flowStat{}
		flows[k] = st
	}
	st.packets++
	st.bytes += n
	if sni != "" {
		st.sni = sni
	}
}

// extractSNI returns the server name from a TLS ClientHello, or "".
//
// It returns "" for almost every packet, which is expected: only ClientHello
// is dissected (there is no ServerHello decoder), only on the ports gopacket
// maps to TLS, and a ClientHello split across TCP segments is never
// reassembled. Treat a hit as a bonus label, never as something to rely on.
func extractSNI(pkt gopacket.Packet) string {
	tls, _ := pkt.Layer(layers.LayerTypeTLS).(*layers.TLS)
	if tls == nil {
		return ""
	}
	for i := range tls.Handshake {
		if sni := tls.Handshake[i].ClientHello.SNI; len(sni) > 0 {
			return string(sni)
		}
	}
	return ""
}

// direction reports which way a flow was going relative to this machine.
func direction(local *map[netip.Addr]struct{}, src, dst netip.Addr) string {
	if local == nil {
		return ""
	}
	_, s := (*local)[src]
	_, d := (*local)[dst]
	switch {
	case s && d:
		return "local"
	case s:
		return "out"
	case d:
		return "in"
	default:
		return ""
	}
}

// localAddrs collects this machine's unicast addresses across every
// interface — a VPN's utun address is still this machine.
func localAddrs() (map[netip.Addr]struct{}, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	out := make(map[netip.Addr]struct{})
	for _, ifc := range ifaces {
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			default:
				continue
			}
			if addr, ok := netip.AddrFromSlice(ip); ok {
				out[addr.Unmap()] = struct{}{}
			}
		}
	}
	if len(out) == 0 {
		return nil, errors.New("no local addresses found")
	}
	return out, nil
}

// endpoint formats an address for the wire, bracketing IPv6.
func endpoint(a netip.Addr, port uint16) string {
	if !a.IsValid() {
		return ""
	}
	if port == 0 {
		return a.String()
	}
	return net.JoinHostPort(a.String(), fmt.Sprint(port))
}

func protoName(p uint8) string {
	switch layers.IPProtocol(p) {
	case layers.IPProtocolICMPv4:
		return "icmp"
	case layers.IPProtocolICMPv6:
		return "icmp6"
	default:
		return layers.IPProtocol(p).String()
	}
}

// CaptureDevice returns the interface capture was started on, or "" if
// StartCapture was never called.
func CaptureDevice() string { return captureDev }
