package network

import (
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/gopacket/gopacket/pcap"
)

// ErrCaptureUnavailable reports that live capture is not running. On macOS
// that means /dev/bpf* could not be opened — capture needs elevated
// privileges, unlike the connection table, which does not.
var ErrCaptureUnavailable = errors.New("packet capture unavailable: needs elevated privileges (run with sudo)")

// maxBuffered caps how many packets are held between sampler ticks. A busy
// interface produces thousands per second; without a cap the buffer would
// grow without bound and each frame would be too large for the socket.
// Overflow is counted, not silently ignored.
const maxBuffered = 200

// Packet is one captured packet, reduced to the fields worth putting on the
// wire. gopacket.Packet itself does not marshal to useful JSON.
type Packet struct {
	TS    int64  `json:"ts"` // Unix milliseconds
	Src   string `json:"src"`
	Dst   string `json:"dst"`
	Proto string `json:"proto"`
	Bytes int    `json:"bytes"`
}

var (
	mu       sync.Mutex
	buffered []Packet
	dropped  int

	startErr error // set once by StartCapture; returned by every drain
)

// StartCapture opens the device and begins capturing in the background. Call
// it once at startup. The returned error is also retained, so GetLivePackets
// keeps reporting the same failure rather than looking merely idle.
//
// It does not block: capture runs in its own goroutine.
func StartCapture(device string) error {
	handle, err := pcap.OpenLive(device, 1600, true, pcap.BlockForever)
	if err != nil {
		startErr = fmt.Errorf("%w: %v", ErrCaptureUnavailable, err)
		return startErr
	}

	go func() {
		defer handle.Close()

		src := gopacket.NewPacketSource(handle, handle.LinkType())
		for pkt := range src.Packets() {
			p := describe(pkt)

			mu.Lock()
			if len(buffered) < maxBuffered {
				buffered = append(buffered, p)
			} else {
				dropped++
			}
			mu.Unlock()
		}
	}()

	log.Printf("network: capturing on %s", device)
	return nil
}

// GetLivePackets returns the packets seen since the previous call and clears
// the buffer, so each frame is a delta rather than a running total.
//
// It returns promptly — it only drains what the capture goroutine has already
// collected. The sampler's loop is shared by every connected client, so this
// must never wait on capture.
func GetLivePackets() ([]Packet, error) {
	if startErr != nil {
		return nil, startErr
	}

	mu.Lock()
	out := buffered
	buffered = nil
	n := dropped
	dropped = 0
	mu.Unlock()

	if n > 0 {
		log.Printf("network: dropped %d packets (buffer full at %d)", n, maxBuffered)
	}
	if out == nil {
		// Non-nil so the JSON is [] rather than null.
		out = []Packet{}
	}
	return out, nil
}

// describe reduces a decoded packet to the fields we serialize.
func describe(pkt gopacket.Packet) Packet {
	p := Packet{
		TS:    time.Now().UnixMilli(),
		Proto: "?",
	}

	if md := pkt.Metadata(); md != nil {
		if !md.Timestamp.IsZero() {
			p.TS = md.Timestamp.UnixMilli()
		}
		// Length is the true size on the wire; CaptureLength is bounded by
		// the snaplen we passed to OpenLive.
		p.Bytes = md.Length
	}

	var srcIP, dstIP string
	switch nl := pkt.NetworkLayer().(type) {
	case *layers.IPv4:
		srcIP, dstIP = nl.SrcIP.String(), nl.DstIP.String()
	case *layers.IPv6:
		srcIP, dstIP = nl.SrcIP.String(), nl.DstIP.String()
	}

	switch tl := pkt.TransportLayer().(type) {
	case *layers.TCP:
		p.Proto = "tcp"
		// Cast to uint16: TCPPort.String() renders "443(https)".
		p.Src = fmt.Sprintf("%s:%d", srcIP, uint16(tl.SrcPort))
		p.Dst = fmt.Sprintf("%s:%d", dstIP, uint16(tl.DstPort))
		return p
	case *layers.UDP:
		p.Proto = "udp"
		p.Src = fmt.Sprintf("%s:%d", srcIP, uint16(tl.SrcPort))
		p.Dst = fmt.Sprintf("%s:%d", dstIP, uint16(tl.DstPort))
		return p
	}

	// No transport layer (ICMP, ARP, …) — report the addresses we have.
	if l := pkt.Layer(layers.LayerTypeICMPv4); l != nil {
		p.Proto = "icmp"
	} else if l := pkt.Layer(layers.LayerTypeICMPv6); l != nil {
		p.Proto = "icmp6"
	} else if l := pkt.Layer(layers.LayerTypeARP); l != nil {
		p.Proto = "arp"
	}
	p.Src, p.Dst = srcIP, dstIP
	return p
}
