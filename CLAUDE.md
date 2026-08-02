# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

A personal network monitoring tool. It watches **this machine's own** network activity and streams it to a browser dashboard in real time. Single-user, local-only — there is no multi-tenancy and no remote agent.

A background sampler polls the system on a fixed interval and pushes frames to the frontend over a WebSocket. The frontend consumes that stream and draws live charts.

Four things are monitored:

- **Per-connection live view** — remote host, port, owning application, connection state.
- **Throughput over time** — derived by diffing consecutive IO counter readings.
- **Per-application rollups** — connections and traffic grouped by owning process.
- **Remote-endpoint enrichment** — hostname and geo for remote IPs.

## Project state

Early. The Go/Gin backend boots, serves the routes below, and streams over a WebSocket. Of the four features above, **only the connection listing is implemented.**

- `internal/api/helpers/network/livePackets.go` — `GetLivePackets()` is a **stub returning an empty slice**, and it is the only source wired into the sampler. The socket currently broadcasts `{"type":"packets","ts":…,"data":[]}` every tick. The user is implementing this; do not fill it in unless asked.
- `user_routes.go` is leftover scaffold returning fake users. It is not part of the product and predates the Go rewrite.
- `client/` is an untouched Vite scaffold — no API calls, no WebSocket consumer yet.
- `infra/*.tf` are empty placeholders.

The backend was ported from Python/FastAPI. That implementation is preserved in git at `e793086` if you need to check how something worked; it is not on disk.

## Commands

Backend (from `server/`):

```bash
go run ./cmd/server        # starts on 127.0.0.1:8000
go build ./...
go vet ./...
go test -race ./...        # tests live in internal/ws and internal/core
```

Frontend (from `client/`):

```bash
bun install
bun run dev        # Vite dev server on :5173
bun run build      # tsc -b && vite build
bun run lint
```

Bun is the client package manager (`bun.lock`, no npm or yarn lockfile). The client has no test runner.

## Privileges — this differs from the Python implementation

**Verified on this machine: `gopsutil`'s `net.Connections("inet")` works without elevated privileges.** It returned 10–15 real established connections running as the normal user.

This is a genuine behavioral difference from the psutil version, which raised `AccessDenied` and forced the whole app to run under `sudo`. gopsutil shells out to `lsof` on macOS rather than using the restricted syscall. **Do not add `sudo` to the run instructions for the connection features** — it is not needed and was only ever a psutil constraint.

Unverified, but expected: running as root lists connections owned by *other* users too, where unprivileged sees only your own.

Packet capture is the exception. Raw capture on macOS requires access to `/dev/bpf*`, which does need elevated privileges. If `GetLivePackets` grows a libpcap/`gopacket` implementation, expect the privilege requirement to return **for that feature only**. `gopacket` is not currently a dependency.

`ErrConnectionsUnavailable` → HTTP 503 is still wired up in `connections.go` for the case where a permission error does occur; it is simply not the normal path.

## Data collection conventions

These are correctness requirements, not style preferences. Each corresponds to a real failure mode.

**Throughput is a difference, never a reading.** IO counters are cumulative since boot. A single sample carries no rate information. Retain the previous reading plus its timestamp and compute the delta over the elapsed interval. Use the *measured* elapsed time, not `SAMPLE_INTERVAL` — the loop drifts under load, and dividing by the nominal value silently skews every rate.

**Guard every process lookup** for the owning-process name. Processes routinely exit between the moment the connection table is read and the moment you look up the PID. This is a normal race, not an exceptional case. A connection whose process has vanished should still be reported, with the app name left unknown.

**Check `Raddr.IP` before using it.** Listening sockets have no remote peer — gopsutil reports an empty `IP` string. `connections.go` filters these out; anything new reading `Raddr` must do the same check.

**Keep DNS resolution off the sampler.** Reverse lookups are slow and blocking, and the sampler is a fixed-interval loop shared by every connected client — a handful of unresolvable IPs stalls the whole stream, not just one frame. Resolve out of band, cache by IP, and **cache negative results too**; unresolvable addresses are common and retrying them every tick is the expensive path. Emit frames with raw IPs immediately and let hostnames arrive in a later frame.

**A sampler `Source` must return promptly.** Same reason. Anything that captures continuously (packets) should buffer in its own goroutine, with the `Source` draining that buffer rather than capturing synchronously.

**Sources return a delta, not a cumulative total.** The sampler broadcasts whatever comes back, once per tick; consumers treat frames as increments.

## Storage

**No database.** Recent frames live in a bounded in-memory buffer in the hub (`historyLimit`, 300 frames **per stream type**). Eviction is automatic and memory is capped. History does not survive a restart, and that is the accepted tradeoff.

Don't add an ORM or a migration tool to satisfy a short-term need — the bounded buffer is the current design, not a placeholder for one.

## Backend architecture

Module `github.com/lavale1012/net-monitor/server`, rooted at `server/`. Run all Go commands from there.

```text
server/
├── .env                                     # config, gitignored
├── cmd/server/main.go                       # wiring: config, hub, samplers, routes, shutdown
└── internal/
    ├── core/config.go                       # Settings, loaded from .env
    ├── ws/                                  # transport: hub, client pumps, sampler
    └── api/
        ├── routes/                          # HTTP + WS handlers
        └── helpers/network/                 # collectors (connections, packets)
```

**Configuration** is centralized in `internal/core/config.go`, loaded from `server/.env` with case-insensitive keys. `CORS_ORIGINS` must be **JSON** (`["http://localhost:5173"]`) — a malformed value logs and falls back to the default rather than parsing as one origin. `SAMPLE_INTERVAL` is a Go duration string (`1s`, `250ms`); malformed *and* non-positive values fall back, because `time.NewTicker` panics on `<= 0`.

`internal/core` is imported only by `main.go`. Settings are threaded to other packages as individual values, not as a `*Settings`.

**Routes.** Each `Register*Routes` function in `internal/api/routes/` owns its own subgroup, and `main.go` mounts them under `settings.APIPrefix`. A full path is `APIPrefix + group + route`. Add a route by writing the registrar and adding one call in `main.go`.

Note an inconsistency: `RegisterUserRoutes(rg)` and `RegisterNetworkRoutes(rg)` take only the group, while `RegisterWSRoutes(rg, hub, origins)` takes two extra positional deps. If a third registrar needs config, introduce a `Deps` struct rather than adding another positional parameter.

**The sampler is owned by the process, not by connections.** One hub and one sampler per stream, started in `main.go` and stopped by context cancellation on SIGINT/SIGTERM. It samples the machine, so it runs regardless of how many clients are attached — a sampler that stopped with the last client would lose the history the buffer exists to retain.

**Hub concurrency.** `Hub.clients` and `Hub.history` are owned exclusively by `Run`'s goroutine and are deliberately unsynchronized. The only cross-goroutine read is `ClientCount()`, served by an `atomic.Int64`. **Do not add a mutex back** — the invariant is that nothing outside `Run` touches those fields. If you add a method that needs them, route it through a channel into `Run`'s select, or extend the atomic pattern. `go test -race ./...` covers this.

A client whose send buffer is full is dropped rather than allowed to stall the hub and every other client behind it.

**Frame format.** Everything on the socket is:

```json
{"type": "packets", "ts": 1785705329723, "data": [...]}
{"type": "packets", "ts": 1785705329723, "error": "needs elevated privileges"}
```

`type` routes the frame to a stream; history is bucketed by it, so a fast sampler cannot evict a slow one's frames from a new client's backfill. `data` and `error` are mutually exclusive in practice (`omitempty` on both).

**Error frames are emitted on transition, not per tick.** A persistently failing source would otherwise fill its history with identical frames and evict the last good data. This is why a source must return an error rather than an empty result when it *cannot read* — "no data" and "cannot read" are different states, and showing an empty list for both is indistinguishable from a healthy idle machine.

**Backfill ordering:** frames are ordered within each stream, but streams interleave in map-iteration order. Don't rely on a globally sorted replay; sort by `ts` client-side if needed.

### Verifying routes

Gin prints every registered route at startup in debug mode — that listing is authoritative. `ENDPOINTS.md` (gitignored) is a written snapshot of it. Verify behavior with real requests against a running server.

## Environment notes

- Go 1.26.3, darwin/arm64.
- `server/.env` and `.venv/` are gitignored at the repo root. `.env` is **not** in git — recreate it from the keys listed in `internal/core/config.go` if it goes missing.
- This machine's disk has run near capacity (97%, ~6 GB free). When it is that full, process startup and file I/O stall unpredictably — commands that normally take a second have taken minutes. If builds or servers seem to hang with ~0% CPU, check `df -h` before debugging the code.
