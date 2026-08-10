package network

import (
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/process"
)

// procTTL bounds how long a PID→name mapping is trusted. The OS recycles
// PIDs, so an unbounded cache would eventually attribute a connection to a
// process that has since been replaced.
const procTTL = 30 * time.Second

type procEntry struct {
	name string
	at   time.Time
}

var (
	procMu    sync.Mutex
	procCache = make(map[int32]procEntry)
)

// NameForPID returns the owning application's name, or "" when it cannot be
// determined.
//
// Every failure degrades to "" rather than an error. A process routinely
// exits between the moment the connection table is read and the moment its
// PID is looked up — that is a normal race, not an exceptional case. The
// connection is still real and should still be reported, just with an
// unknown owner.
//
// Results are cached because the sampler re-reads the same PIDs every tick,
// and a process lookup shells out on macOS.
func NameForPID(pid int32) string {
	if pid <= 0 {
		return ""
	}

	procMu.Lock()
	e, ok := procCache[pid]
	procMu.Unlock()
	if ok && time.Since(e.at) < procTTL {
		return e.name
	}

	// Resolve outside the lock: this blocks on a syscall, and holding the
	// mutex across it would serialize every other lookup behind it.
	name := lookupProcName(pid)

	procMu.Lock()
	procCache[pid] = procEntry{name: name, at: time.Now()}
	procMu.Unlock()

	return name
}

// lookupProcName does the actual lookup. Both failure modes — the process
// having exited, and insufficient permission to inspect it — resolve to "".
func lookupProcName(pid int32) string {
	p, err := process.NewProcess(pid)
	if err != nil {
		return "" // exited between the table read and now
	}
	name, err := p.Name()
	if err != nil {
		return "" // not permitted to inspect this process
	}
	return name
}
