# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

A network monitoring product. Users sign up on a hosted site, install an **agent** on their own machine, and watch that machine's network activity live in a browser dashboard.

**The agent is the only thing that touches network state.** It runs on the user's machine, where "this machine" correctly means *their* machine. The hosted service never collects anything itself — it authenticates people, routes each agent's frames to that user's browsers, and serves the dashboard. A server-side collector would report the *server's* network to every user, which is the whole reason for the agent.

Four things are monitored, plus packet capture:

- **Per-connection live view** — remote host, port, owning application, connection state.
- **Throughput over time** — derived by diffing consecutive IO counter readings.
- **Per-application rollups** — connections grouped by owning process.
- **Remote-endpoint enrichment** — reverse-DNS hostnames for remote IPs.
- **Packet flows** — per-conversation packet/byte counts, direction, and TLS SNI where visible.

## Project state

This started as a single-user local-only tool and is **mid-migration** to the hosted agent model. Read the layout below as "what exists," not "what is planned."

Working, verified against a live process:

- All five collectors, both WebSocket streams, and the .NET gateway proxying to the agent. The agent runs standalone today: it collects, samples, and serves its own HTTP + WebSocket on `127.0.0.1:8000`.

Not built:

- **There is no cloud service.** No `cmd/cloud`, no agent enrollment, no outbound transport. The agent still *serves* browsers directly instead of *pushing* to a hosted service. Reversing that is the main outstanding work.
- **There is no authentication.** `gateway/Endpoints/AuthEndpoints.cs` is a stub — `/login` echoes the username back as success, `/me` returns `"not implemented"`, and every proxied route is `AuthorizationPolicy: anonymous`.
- **`client/` is an untouched Vite scaffold.** `App.tsx` renders an empty fragment; no API call, no WebSocket consumer, no chart.
- **`infra/*.tf` are empty placeholders** (0 bytes).
- **`AppRollup.BytesSent`/`BytesRecv` are always 0.** The connection table carries no per-connection byte counts; filling them means joining against captured flows by 4-tuple.
- **The capture interface is hardcoded to `en0`** in `cmd/agent/main.go`.

### Migration phases

1. ~~Split the module so agent-side code is its own thing~~ — **done**; that is the `internal/agent/` tree below.
2. **Real identity** — user store, password hashing, sessions. Replace the stub. Routing means nothing without this.
3. **Enrollment + inverted transport** — token issuance, agent credential storage, outbound WebSocket from agent to cloud, cloud-side routing keyed by user, agent id on frames so one user with two machines can tell them apart.
4. **The dashboard.**

## What the hosted direction changes

Three things are correct today *because* the app is local-only, and become defects the moment it is hosted. Do not treat them as done.

**The hub has no concept of who a frame belongs to.** `Hub.broadcast` fans every frame to every connected client — one sampler, one history buffer, all clients. Add a login on top of this and two users get byte-identical streams of whichever machine is running the collectors. Tenancy is not a layer to add on top: frames need a tenant key and `history` needs to bucket by it.

**The WebSocket origin check deliberately allows requests with no `Origin` header.** See `originChecker` in `agent/api/routes/ws_routes.go`. That is right for a local tool — curl and websocat have no origin to forge, and the check exists only to stop a random web page opening a socket to localhost. Hosted, that same line means anyone reaching the endpoint with curl bypasses the only check on the socket. Connection-level auth must land before that endpoint is public; origin checking stops being a boundary at all.

**Collector state is package-level globals** — `flows`, `prev`, `hostCache`, `procCache`, `localIPs`, `captureDev`. This is *fine and intended* while collectors only ever run in the agent, because one agent process really does mean one machine. It becomes wrong the instant anything runs a collector server-side. Don't "fix" it by making collectors instance-based; keep collectors out of the cloud service instead.

Also worth holding onto: the product makes you custodian of other people's connection metadata — which hosts they talk to, which apps are running. That raises the bar on anything touching auth, storage, or retention.

## Repo layout

```text
net-monitor/
├── server/          Go — the agent (module github.com/lavale1012/net-monitor/server)
├── gateway/         .NET 10 — YARP reverse proxy + auth surface, port 5080
├── client/          Vite + React 19 dashboard (scaffold only)
└── infra/           empty Terraform placeholders
```

The Go module is still rooted at `server/` and still named `.../net-monitor/server`. The directory name is now a misnomer — it holds the agent. Renaming it and the module path is a purely mechanical follow-up, deliberately deferred so the restructure diff stayed reviewable.

```text
server/
├── .env                              # config, gitignored
├── cmd/agent/main.go                 # wiring: config, hub, samplers, routes, shutdown
└── internal/
    ├── core/config.go                # Settings, loaded from .env — shared
    ├── ws/                           # transport: hub, client pumps, sampler
    └── agent/
        ├── network/                  # the collectors — agent-side only, never cloud-side
        └── api/
            ├── routes/               # URL surface
            └── handlers/             # thin HTTP + WS handlers
```

`internal/agent/` is the boundary that matters: **anything under it runs on the user's machine.** When the cloud service arrives it gets `internal/cloud/`, and `internal/ws` splits — `Sampler` is agent-side, `Hub` and `Client` are cloud-side.

## Commands

Agent (from `server/`):

```bash
go run ./cmd/agent         # starts on 127.0.0.1:8000
go build ./...
go vet ./...
go test -race ./...        # tests live in internal/ws and internal/core
```

Gateway (from `gateway/`):

```bash
dotnet run                 # http://localhost:5080, proxies to the agent
```

Frontend (from `client/`):

```bash
bun install
bun run dev                # Vite dev server on :5173
bun run build              # tsc -b && vite build
bun run lint
```

Bun is the client package manager (`bun.lock`, no npm or yarn lockfile). The client has no test runner.

Start the agent before the gateway — the gateway proxies to it and has no fallback if it is down.

## Privileges

**The four gopsutil collectors need no elevated privileges.** `net.Connections("inet")` shells out to `lsof` on macOS rather than using the restricted syscall, and returns real established connections as a normal user. This is a genuine difference from the Python/psutil original, which raised `AccessDenied` and forced the whole app under `sudo`. **Do not add `sudo` to the run instructions** — it is not needed and was only ever a psutil constraint.

`ErrConnectionsUnavailable` → HTTP 503 is still wired up in `connections.go` for the case where a permission error does occur; it is not the normal path.

**Packet capture is the exception** — it opens `/dev/bpf*`, which is root-owned. On this machine it nonetheless runs **unprivileged**, because Wireshark's `ChmodBPF` LaunchDaemon (`/Library/LaunchDaemons/org.wireshark.ChmodBPF.plist`) chowns `/dev/bpf*` to the `access_bpf` group at boot and the user is a member. Verify with `id -Gn | grep access_bpf`. Running under `sudo` also works, but then the whole process is root — and as root the connection table lists *other users'* connections too.

Capture failing is not fatal: `main.go` logs it and continues, the `packets` stream reports its own outage over the socket, and `snapshot` keeps working. Keep it that way — capture is the one feature with an install-friction requirement, and it should never be able to take the other four down.

## Data collection conventions

These are correctness requirements, not style preferences. Each corresponds to a real failure mode. All of them apply to agent-side code.

**Throughput is a difference, never a reading.** IO counters are cumulative since boot. A single sample carries no rate information. Retain the previous reading plus its timestamp and compute the delta over the elapsed interval. Use the *measured* elapsed time, not `SAMPLE_INTERVAL` — the loop drifts under load, and dividing by the nominal value silently skews every rate.

**Guard every process lookup** for the owning-process name. Processes routinely exit between the moment the connection table is read and the moment you look up the PID. This is a normal race, not an exceptional case. A connection whose process has vanished should still be reported, with the app name left unknown.

**Check `Raddr.IP` before using it.** Listening sockets have no remote peer — gopsutil reports an empty `IP` string. `connections.go` filters these out; anything new reading `Raddr` must do the same check.

**Keep DNS resolution off the sampler.** Reverse lookups are slow and blocking, and the sampler is a fixed-interval loop shared by every connected client — a handful of unresolvable IPs stalls the whole stream, not just one frame. Resolve out of band, cache by IP, and **cache negative results too**; unresolvable addresses are common and retrying them every tick is the expensive path. Emit frames with raw IPs immediately and let hostnames arrive in a later frame.

**A sampler `Source` must return promptly.** Same reason. Anything that captures continuously (packets) buffers in its own goroutine, with the `Source` draining that buffer rather than capturing synchronously.

**Sources return a delta, not a cumulative total.** The sampler broadcasts whatever comes back, once per tick; consumers treat frames as increments.

**Known wrinkle:** `CollectThroughput` keeps one package-level `prev` reading shared by the HTTP handler *and* the snapshot sampler. With the sampler running every second, an HTTP request diffs against the sampler's last tick rather than the caller's last request — `elapsedMs` of 2–20 is normal there, and the rate over such a window is noisy. Fine for the stream, near-useless for polling the endpoint.

## Storage

**No database yet.** Recent frames live in a bounded in-memory buffer in the hub (`historyLimit`, 300 frames **per stream type**). Eviction is automatic and memory is capped. History does not survive a restart, and that is the accepted tradeoff for the local tool.

Phase 2 needs real persistence for accounts and agent enrollments — that is the point where a database is genuinely warranted, not before. Whether the cloud *also* persists frames is an open decision with real privacy weight; the bounded in-memory buffer remains a legitimate answer. Don't add an ORM or migration tool ahead of phase 2.

## Architecture

### Configuration

Centralized in `internal/core/config.go`, loaded from `server/.env` with **case-insensitive keys**; a real environment variable takes precedence over the file, and an empty value counts as unset. `CORS_ORIGINS` must be **JSON** (`["http://localhost:5173"]`) — a malformed value logs and falls back to the default rather than parsing as one origin. `SAMPLE_INTERVAL` is a Go duration string (`1s`, `250ms`); malformed *and* non-positive values fall back, because `time.NewTicker` panics on `<= 0`.

`API_PREFIX` is **duplicated in `gateway/appsettings.json`**, which hardcodes `/api/v1` in its YARP route paths. Changing it in one place silently breaks the proxy.

`internal/core` is imported only by `main.go`. Settings are threaded to other packages as individual values, not as a `*Settings`.

### Routes

Each `Register*Routes` function in `internal/agent/api/routes/` owns its own subgroup, and `main.go` mounts them under `settings.APIPrefix`. A full path is `APIPrefix + group + route`. Add a route by writing the registrar and adding one call in `main.go`.

Note an inconsistency: `RegisterNetworkRoutes(rg)` takes only the group while `RegisterWSRoutes(rg, hub, origins)` takes two extra positional deps. If a third registrar needs config, introduce a `Deps` struct rather than adding another positional parameter.

The agent's `/health` sits at the **root**, not under `API_PREFIX`. The gateway exposes it at `/api/v1/stream/health` to keep it distinct from its own `/health`.

### The gateway

.NET 10 minimal API using YARP. It binds `http://localhost:5080` — `localhost` not `127.0.0.1` so Kestrel binds both loopbacks, and 5080 because **macOS AirPlay Receiver occupies 5000 and 7000**. It proxies `/api/v1/network/**`, `/api/v1/live-packets`, and `/api/v1/stream/health` to `127.0.0.1:8000`.

YARP handles WebSocket upgrades itself via `IHttpUpgradeFeature`, so `app.UseWebSockets()` is deliberately absent — that middleware is for *terminating* sockets, which the gateway never does.

The proxy rewrites the `Origin` header to `http://gateway.internal` on the hop. That sentinel is listed in `CORS_ORIGINS` so proxied handshakes are deterministic and don't 403 when a port changes or someone uses `127.0.0.1` instead of `localhost`; the real origins stay listed so direct debugging against `:8000` still works.

### The sampler is owned by the process, not by connections

One hub and one sampler per stream, started in `main.go` and stopped by context cancellation on SIGINT/SIGTERM. They sample the machine, so they run regardless of how many clients are attached — a sampler that stopped with the last client would lose the history the buffer exists to retain.

Two streams, because they are different shapes of data. `packets` is a **delta** — flows seen since the last tick — and must keep draining even with no consumer, or the capture accumulator fills to its cap and discards. `snapshot` is **current state**, and its four parts update together, so they ship in one frame rather than racing each other into the UI as separate streams.

### Hub concurrency

`Hub.clients` and `Hub.history` are owned exclusively by `Run`'s goroutine and are deliberately unsynchronized. The only cross-goroutine read is `ClientCount()`, served by an `atomic.Int64`. **Do not add a mutex back** — the invariant is that nothing outside `Run` touches those fields. If you add a method that needs them, route it through a channel into `Run`'s select, or extend the atomic pattern. `go test -race ./...` covers this.

A client whose send buffer is full is dropped rather than allowed to stall the hub and every other client behind it.

### Frame format

Everything on the socket is:

```json
{"type": "snapshot", "ts": 1786333101439, "data": {...}}
{"type": "packets",  "ts": 1786333101362, "error": "packet capture unavailable: ..."}
```

`type` routes the frame to a stream (`packets` or `snapshot`); history is bucketed by it, so a fast sampler cannot evict a slow one's frames from a new client's backfill. `data` and `error` are mutually exclusive in practice (`omitempty` on both).

**Error frames are emitted on transition, not per tick.** A persistently failing source would otherwise fill its history with identical frames and evict the last good data. This is why a source must return an error rather than an empty result when it *cannot read* — "no data" and "cannot read" are different states, and showing an empty list for both is indistinguishable from a healthy idle machine.

**Backfill ordering:** frames are ordered within each stream, but streams interleave in map-iteration order. Don't rely on a globally sorted replay; sort by `ts` client-side if needed.

When the cloud service lands, keep this envelope. Add identity at the **connection** level, not in the frame body — an agent should not be able to claim another tenant's id by writing it into a payload.

### Verifying routes

Gin prints every registered route at startup in debug mode — that listing is authoritative. `ENDPOINTS.md` (gitignored) is a written snapshot of it and **is currently stale**: it documents `/api/v1/ws`, `GET /`, and user routes that no longer exist. Verify behavior with real requests against a running server.

```bash
curl -s http://127.0.0.1:8000/health
curl -s http://127.0.0.1:8000/api/v1/network/connections | python3 -m json.tool | head -20

# WebSocket. With websocat 1.14 the '=' is required — plain -H 'X: y' <url>
# makes -H swallow the URL and fails with "No URL specified".
websocat -H='Origin: http://localhost:5173' ws://127.0.0.1:8000/api/v1/live-packets
```

## Environment notes

- Go 1.26.3, .NET SDK 10.0.103, Bun 1.3.3, darwin/arm64.
- `server/.env` and `.venv/` are gitignored. `.env` is **not** in git — recreate it from the keys listed in `internal/core/config.go` if it goes missing. Note the default `CORS_ORIGINS` allows only `http://localhost:5173`, which will 403 the gateway's proxied handshake; `http://gateway.internal` must be listed.
- `go build` prints `ld: warning: ignoring duplicate libraries: '-lpcap'` on macOS. Harmless — cgo passes `-lpcap` twice linking gopacket against system libpcap.
- This machine's disk has run near capacity (97%, ~6 GB free). When it is that full, process startup and file I/O stall unpredictably — commands that normally take a second have taken minutes. If builds or servers seem to hang with ~0% CPU, check `df -h` before debugging the code.

## History

The backend was ported from Python/FastAPI. That implementation is preserved in git at `e793086`; it is not on disk. The `sudo` requirement, the `{"data": ...}` envelope, and the connection JSON shape all date from it.
