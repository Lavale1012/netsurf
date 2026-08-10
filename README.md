# net-monitor

A network monitor. An agent watches a machine's own network activity and streams it to a browser dashboard in real time.

The goal is a hosted site: users sign up, install the agent on their own machine, and watch it live in the browser. **The agent is the only thing that touches network state** — it runs on the user's machine, where "this machine" correctly means *theirs*. The hosted service never collects anything itself; a server-side collector would report the *server's* network to every user, which is the whole reason the agent exists.

That service does not exist yet. Today the agent runs standalone: it collects, samples on a fixed interval, and serves its own HTTP and WebSocket endpoints on loopback. Recent frames live in a bounded in-memory buffer and are replayed to each new client. See [Project status](#project-status) for exactly where the line sits.

Four things are monitored:

| Feature | What it reports |
| --- | --- |
| **Per-connection live view** | Remote host, port, owning PID, connection state |
| **Throughput over time** | Bytes/sec up and down, derived by diffing IO counters |
| **Per-application rollups** | Connections grouped by owning process |
| **Remote-endpoint enrichment** | Reverse-DNS hostnames for remote IPs |

Plus live **packet flows** captured off the wire — per-conversation packet and byte counts, direction, and TLS SNI where it can be seen.

## Architecture

Three processes today, all on one machine. Only the agent touches network state; the gateway is a reverse proxy in front of it, and the client is the dashboard.

```text
browser (Vite :5173)
      │
      ▼
ASP.NET gateway (:5080)         gateway/   — YARP reverse proxy, auth surface
      │  proxies /api/v1/network/**, /api/v1/live-packets, /api/v1/stream/health
      ▼
agent (127.0.0.1:8000)          server/    — collectors, hub, samplers, WebSocket
      │
      ▼
gopsutil (connections, IO counters, processes) + libpcap (packet capture)
```

The agent binds to `127.0.0.1` only. The gateway binds to `localhost` (both loopbacks) on `:5080`, which is the address the dashboard is meant to talk to.

Where this is heading — the agent stops *serving* browsers and starts *pushing* to a hosted service, because it sits behind NAT and has to make the outbound connection:

```text
user's machine                              cloud
┌────────────────┐                    ┌──────────────────┐
│ agent          │ ──outbound WS───▶  │ auth + routing   │
│ collect+sample │   authenticated    │ per-user fan-out │
└────────────────┘                    └────────┬─────────┘
                                               │
                             browser ──────────┘  (login)
```

```text
server/
├── .env                              # config, gitignored
├── cmd/agent/main.go                 # wiring: config, hub, samplers, routes, shutdown
└── internal/
    ├── core/config.go                # Settings, loaded from .env — shared
    ├── ws/                           # transport: hub, client pumps, sampler
    └── agent/                        # everything under here runs on the user's machine
        ├── network/                  # collectors (connections, packets, throughput, apps, hostnames)
        └── api/
            ├── routes/               # URL surface
            └── handlers/             # thin HTTP + WS handlers
```

`internal/agent/` is the boundary that matters. When the cloud service lands it gets `internal/cloud/`, and `internal/ws` splits — `Sampler` is agent-side, `Hub` and `Client` are cloud-side.

The Go module is still rooted at `server/` and named `.../net-monitor/server`. That directory name is now a misnomer; renaming it and the module path is a mechanical follow-up, deliberately deferred to keep the restructure diff reviewable.

## Project status

This began as a single-user local-only tool and is **mid-migration** to the hosted agent model. Read the sections below as "what exists," not "what is planned."

Working end to end: all five collectors, both WebSocket streams, and the gateway proxy — every output in this README was captured from a live process, not read off the source.

Not built yet:

- **There is no cloud service.** No `cmd/cloud`, no enrollment, no outbound transport. The agent still *serves* browsers directly instead of *pushing* to a hosted service. Reversing that is the main outstanding work.
- **There is no authentication.** [`AuthEndpoints.cs`](gateway/Endpoints/AuthEndpoints.cs) is a stub: `POST /api/v1/auth/login` echoes the username back as success, `/me` returns `"not implemented"`, and every proxied route is `AuthorizationPolicy: anonymous`.
- **`client/` is an untouched Vite scaffold.** `App.tsx` renders an empty fragment. There is no API call, no WebSocket consumer, and no chart.
- **`infra/*.tf` are empty placeholders** (0 bytes).
- **`AppRollup.BytesSent` / `BytesRecv` are always `0`.** The connection table carries no per-connection byte counts; filling these means joining against captured flows by 4-tuple, which is a separate step.
- **The capture interface is hardcoded to `en0`** in `cmd/agent/main.go`. It is not configurable via `.env` yet.

### Roadmap

1. ~~Split the module so agent-side code is its own thing.~~ **Done** — that is the `internal/agent/` tree above.
2. **Real identity** — user store, password hashing, sessions. Routing means nothing without it.
3. **Enrollment + inverted transport** — token issuance, agent credentials, outbound WebSocket, cloud-side routing keyed by user, and an agent id on frames so one user with two machines can tell them apart.
4. **The dashboard.**

Two things are correct *because* the app is local-only today and become defects the moment it is hosted, so they are tracked rather than assumed done: the hub fans every frame to every client with no tenant key, and the WebSocket origin check deliberately allows requests with no `Origin` header. Both are covered in detail under [WebSocket stream](#websocket-stream).

## Requirements

| Tool | Version here | Notes |
| --- | --- | --- |
| Go | 1.26.3 (darwin/arm64) | `server/` |
| .NET SDK | 10.0.103 | `gateway/`, targets `net10.0` |
| Bun | 1.3.3 | `client/` package manager — `bun.lock`, no npm or yarn lockfile |
| libpcap | macOS system library | linked via cgo by `gopacket/pcap`; no install step needed |
| websocat | optional | only for poking the socket from the terminal |

Developed and verified on macOS (darwin/arm64). The collectors are gopsutil-backed and portable in principle, but the privilege notes below are macOS-specific.

## Privileges

**The connection features need no elevated privileges.** `gopsutil`'s `net.Connections("inet")` shells out to `lsof` on macOS rather than using the restricted syscall, and returns real established connections as a normal user. This is a genuine difference from the earlier Python/psutil implementation, which raised `AccessDenied` and forced the whole app under `sudo`. **Do not add `sudo` to the run instructions** — it is not needed.

**Packet capture is the exception.** It opens `/dev/bpf*`, which is root-owned:

```console
$ ls -l /dev/bpf0
crw-rw----  1 root  access_bpf  ... /dev/bpf0
```

Two ways to satisfy it — the first is what this machine uses, and it is the better one:

1. **Join the `access_bpf` group** (recommended). Wireshark's `ChmodBPF` LaunchDaemon (`/Library/LaunchDaemons/org.wireshark.ChmodBPF.plist`) sets this up: it chowns `/dev/bpf*` to `access_bpf` at boot and adds you to the group. Verify with `id -Gn | grep access_bpf`. With this in place the server runs capture **unprivileged**, as a normal user.
2. **Run the server under `sudo`.** Works, but then the whole process is root — and note that as root the connection table lists connections owned by *other* users too, not just yours.

Capture failing is **not** fatal. `main.go` logs the error and carries on; the `packets` stream reports its own outage over the socket while `snapshot` keeps working:

```json
{"type":"packets","ts":1786333101362,"error":"packet capture unavailable: needs elevated privileges (run with sudo)"}
```

## Installation

```bash
git clone https://github.com/Lavale1012/net-monitor.git
cd net-monitor
```

### Server (Go)

```bash
cd server
go mod download
go build ./...
```

`server/.env` is **gitignored and not in the repo** — create it before first run. These are the current values:

```bash
cat > server/.env <<'EOF'
API_PREFIX=/api/v1
CORS_ORIGINS=["http://gateway.internal","http://localhost:5173","http://127.0.0.1:5173","http://localhost:5080","http://127.0.0.1:5080"]
PORT=8000
SAMPLE_INTERVAL=1s
EOF
```

A missing `.env` is not an error — every key has a default (see [Environment variables](#environment-variables)) — but the default `CORS_ORIGINS` allows only `http://localhost:5173`, which will make the gateway's proxied WebSocket handshake fail with 403.

### Gateway (.NET)

```bash
cd gateway
dotnet restore
dotnet build
```

### Client (Bun)

```bash
cd client
bun install
```

## Running

Start the agent first — the gateway proxies to it and has no fallback if it is down.

```bash
# terminal 1 — agent on 127.0.0.1:8000
cd server && go run ./cmd/agent

# terminal 2 — gateway on http://localhost:5080
cd gateway && dotnet run

# terminal 3 — Vite dev server on http://localhost:5173
cd client && bun run dev
```

Startup log from the agent, confirming capture came up and listing the route table (Gin's own dump is the authoritative listing):

```text
network: capturing on en0
[GIN-debug] GET /health                       --> main.main.func3
[GIN-debug] GET /api/v1/network/connections   --> handlers.ListConnections
[GIN-debug] GET /api/v1/network/throughput    --> handlers.GetThroughput
[GIN-debug] GET /api/v1/network/hostnames     --> handlers.GetHostnames
[GIN-debug] GET /api/v1/network/apps          --> handlers.GetApps
[GIN-debug] GET /api/v1/live-packets          --> routes.RegisterWSRoutes.func1
net-monitor listening on http://127.0.0.1:8000 (ws at /api/v1/live-packets)
```

Every route is `GET`. There are no write endpoints on the agent — it only reads the machine's state. `Ctrl-C` cancels the context, stops both samplers, and shuts the HTTP server down with a 5-second grace period.

### Route map

`{prefix}` is `API_PREFIX`, default `/api/v1`.

| Path | Via gateway `:5080` | Direct `:8000` | Returns |
| --- | --- | --- | --- |
| `/health` | gateway's own liveness | agent + hub client count | `{"status":"ok",...}` |
| `{prefix}/stream/health` | ✅ → rewrites to agent `/health` | — | agent liveness |
| `{prefix}/network/connections` | ✅ | ✅ | Established connections |
| `{prefix}/network/throughput` | ✅ | ✅ | Current up/down rate |
| `{prefix}/network/hostnames` | ✅ | ✅ | Resolved remote hostnames |
| `{prefix}/network/apps` | ✅ | ✅ | Per-process rollups |
| `{prefix}/live-packets` | ✅ (WebSocket) | ✅ (WebSocket) | Live frame stream |
| `{prefix}/auth/login`, `/logout`, `/me` | gateway only (stub) | — | Not implemented |

Note the agent's `/health` sits at the **root**, not under `API_PREFIX`. The gateway exposes it at `{prefix}/stream/health` to keep it distinct from its own `/health`.

## Usage examples

### Health

```console
$ curl -s http://localhost:5080/health
{"status":"ok","service":"gateway"}

$ curl -s http://localhost:5080/api/v1/stream/health
{"clients":0,"status":"ok"}
```

`clients` is the number of WebSocket clients currently attached, straight from the hub's atomic counter — useful for confirming a dashboard actually connected.

### Connections

```bash
curl -s http://localhost:5080/api/v1/network/connections
```

```json
{"data":[
  {"laddr":{"ip":"fe80:b::1cb0:ac13:bdfc:14c2","port":58431},
   "raddr":{"ip":"fe80:b::c8a:6937:c147:9f12","port":51696},
   "status":"ESTABLISHED",
   "pid":628}
]}
```

Established inet connections only. Listening sockets have no remote peer and are filtered out, so `status` is always `"ESTABLISHED"` today. `data` is always a non-nil array — an idle machine returns `{"data":[]}`, never `{"data":null}`.

### Throughput

```console
$ curl -s http://localhost:5080/api/v1/network/throughput
{"data":{"bytesSentPerSec":1025.2768434100058,"bytesRecvPerSec":1029.361611710444,"elapsedMs":979}}
```

Throughput is always a **difference between two counter reads**, never a single reading — IO counters are cumulative since boot. `elapsedMs` is the *measured* interval the rate was computed over, not `SAMPLE_INTERVAL`; the sampler loop drifts under load and dividing by the nominal value would silently skew every rate.

Two consequences worth knowing:

- **The first read after startup returns zeros.** There is nothing to diff against yet.
- **This HTTP endpoint shares its previous-reading state with the `snapshot` sampler.** With the sampler running every second, an HTTP request lands shortly after a tick and reports the rate over that small remainder — `elapsedMs` of `2` or `22` is normal and the rate over such a short window is noisy. For a stable series, read the `snapshot` stream rather than polling this endpoint.

### Hostnames

```bash
curl -s http://localhost:5080/api/v1/network/hostnames
```

```json
{"data":[
  {"ip":"fe80:b::c8a:6937:c147:9f12","host":"lavale-butterfields-iphone.local"},
  {"ip":"fe80:13::44ce:8598:8553:3465","host":""}
]}
```

`host` is `""` when the IP is not resolved **yet** or is not resolvable at all — the two are deliberately indistinguishable to the caller. Resolution never blocks the sampler: an unknown IP is queued to a background worker and its name appears in a later frame. Results are cached by IP, **including failures** (30 min for hits, 10 min for misses), because most remote IPs have no PTR record and retrying them every tick is the expensive path.

### Per-app rollups

```bash
curl -s http://localhost:5080/api/v1/network/apps
```

```json
{"data":[
  {"app":"claude","pid":29744,"connections":5,"bytesSent":0,"bytesRecv":0},
  {"app":"identityservicesd","pid":642,"connections":3,"bytesSent":0,"bytesRecv":0}
]}
```

Busiest first, with a deterministic tiebreak on PID so dashboard rows do not jump around. `bytesSent`/`bytesRecv` are always `0` — see [Project status](#project-status). A connection whose process has already exited is still counted, with `app` left `""`; that race is normal, not exceptional.

### WebSocket stream

```bash
# through the gateway
websocat -H='Origin: http://localhost:5173' ws://localhost:5080/api/v1/live-packets

# or direct to the agent
websocat -H='Origin: http://localhost:5173' ws://127.0.0.1:8000/api/v1/live-packets
```

> With websocat 1.14, write `-H='Origin: ...'` with the equals sign. The plain `-H 'Origin: ...' <url>` form makes `-H` swallow the URL and fails with `No URL specified`.

Server-push only; the server ignores anything the client sends. It pings every 54s and drops a client that misses a pong for 60s. A client whose 64-frame send buffer fills is dropped rather than allowed to stall the hub and every other client behind it.

**Origin is checked on upgrade** against `CORS_ORIGINS`, because browsers do not apply CORS to WebSocket handshakes — without it any page on the internet could open a socket to your local dashboard and read your traffic. A disallowed origin gets **403** before upgrading; requests with no `Origin` header (curl, websocat, Go clients) are allowed.

```console
$ curl -o /dev/null -w '%{http_code}\n' -H 'Origin: http://evil.example'    ... /api/v1/live-packets
403
$ curl -o /dev/null -w '%{http_code}\n' -H 'Origin: http://localhost:5173'  ... /api/v1/live-packets
101
```

> **Two things here are right for a local tool and wrong for a hosted one.** Both are load-bearing for the [roadmap](#roadmap), so they are called out rather than left to be discovered later.
>
> **Allowing an absent `Origin` is deliberate.** A local non-browser client has nothing to forge, and the check exists only to stop a random web page opening a socket to localhost. Once this endpoint is public, that same line means anyone reaching it with curl bypasses the only check on the socket — connection-level auth has to land first, and origin checking stops being a boundary at all.
>
> **The hub has no tenant key.** One sampler, one history buffer, and every frame goes to every connected client. Put a login in front of this unchanged and two users receive byte-identical streams of whichever machine ran the collectors. Isolation is not a layer to add on top: frames need a tenant key and `history` needs to bucket by it.

#### Frame format

Everything on the socket is one envelope:

```json
{"type":"snapshot","ts":1786333101439,"data":{...}}
{"type":"packets","ts":1786333101362,"error":"packet capture unavailable: ..."}
```

| Field | Meaning |
| --- | --- |
| `type` | Stream name — routes the frame client-side. `"packets"` or `"snapshot"`. |
| `ts` | Unix **milliseconds**. |
| `data` | Payload. Absent on error frames. |
| `error` | Present only while that stream's source is failing. Absent otherwise. |

`data:[]` means "sampled successfully, nothing to report." `error` means "could not sample." Treat them differently in the UI — that distinction is the whole point of the error field. Error frames are emitted **on transition, not every tick**: a persistent failure sends one frame, not one per second, so it cannot fill the history buffer and evict the last good data. A later frame carrying `data` and no `error` means the source recovered.

On connect the server backfills up to **300 retained frames per stream type**, then streams live. Frames are ordered within a stream, but the two streams interleave in map-iteration order — sort by `ts` client-side if you need a globally ordered replay.

#### The `packets` stream

A **delta**: the flows seen since the last tick, not a running total. Consumers add consecutive frames rather than diffing them.

```json
{"type":"packets","ts":1786333101362,"data":[
  {"src":"192.168.88.182:59391","dst":"34.149.66.165:443","proto":"tcp","packets":6,"bytes":7022,"dir":"out"},
  {"src":"160.79.104.10:443","dst":"192.168.88.182:58420","proto":"tcp","packets":1,"bytes":105,"dir":"in"},
  {"src":"192.168.88.182:57102","dst":"17.250.96.108:443","proto":"udp","packets":1,"bytes":83,"dir":"out"}
]}
```

- `dir` is `"in"`, `"out"`, or `"local"`, decided against this machine's own addresses across every interface (a VPN's `utun` address counts as local). The set is refreshed every 30s so a VPN coming up or a DHCP renewal does not silently empty the stream.
- `sni` appears only when a TLS ClientHello was seen on a port gopacket maps to TLS, and is never reassembled across TCP segments. Treat it as a bonus label, never as something to rely on.
- Broadcast and multicast are dropped in-kernel by a BPF filter — on a home network that is most packets by count (mDNS, SSDP, ARP, neighbour discovery) and none of it is this machine talking to the internet.
- At most **100 rows per frame**, biggest talkers first. This multiplies with the 300-frame history, so raising it is not free: ~100 rows is roughly 4MB retained.

A `?device=` query parameter is accepted but only to **reject a mismatch** — one pcap handle is opened at startup and shared by every client, so a request for another interface cannot be served:

```console
$ curl 'http://127.0.0.1:8000/api/v1/live-packets?device=en5'
{"detail":"capture is running on en0, not en5"}   # 400, before the upgrade
```

#### The `snapshot` stream

Current state — what is true right now. Its four parts update together, so they ship in one frame rather than racing each other into the UI as separate streams:

```json
{"type":"snapshot","ts":1786333101439,"data":{
  "connections": [...],
  "throughput":  {"bytesSentPerSec":1025.27,"bytesRecvPerSec":1029.36,"elapsedMs":979},
  "apps":        [...],
  "hostnames":   [...]
}}
```

Each key holds exactly what the matching HTTP endpoint returns inside its `data` envelope. If any one collector fails, the whole frame becomes an error frame for that tick.

## Environment variables

### Server — `server/.env`

Loaded by `internal/core/config.go` via `godotenv`. **Keys are resolved case-insensitively** (`PORT` and `port` both work), and a real environment variable takes precedence over the file. An empty value is treated as unset and falls back to the default. The file is gitignored; recreate it from this table if it goes missing.

| Variable | Default | Description |
| --- | --- | --- |
| `APP_NAME` | `net-monitor` | Name used in the startup log line. Cosmetic. |
| `API_PREFIX` | `/api/v1` | Mount point for every route except `/health`. |
| `PORT` | `8000` | Port on `127.0.0.1`. The host is not configurable — the service is loopback-only by design. |
| `CORS_ORIGINS` | `["http://localhost:5173"]` | **JSON array of strings.** Feeds both the CORS middleware and the WebSocket origin check. |
| `SAMPLE_INTERVAL` | `1s` | Go duration string (`1s`, `250ms`, `2s`) — how often **both** samplers poll. |

Three behaviors worth knowing, each guarding a real failure:

- **`CORS_ORIGINS` must be valid JSON.** A malformed value logs a warning and falls back to the default rather than being parsed as a single origin — otherwise a typo would silently allow one wrong origin and lock out every right one.
- **`SAMPLE_INTERVAL` falls back when malformed *or* non-positive.** `time.NewTicker` panics on anything `<= 0`, so `0s` and `-1s` are rejected the same way `banana` is.
- **`API_PREFIX` is duplicated in the gateway.** `gateway/appsettings.json` hardcodes `/api/v1` in its YARP route paths. Changing it here alone silently breaks the proxy — change both.

**About `gateway.internal` in `CORS_ORIGINS`:** it is a sentinel, not a real host. The gateway's YARP route rewrites the `Origin` header to `http://gateway.internal` on the proxy hop, so proxied handshakes are deterministic and do not 403 when a port changes or someone uses `127.0.0.1` instead of `localhost`. The real origins stay listed so direct debugging against `:8000` still works.

### Gateway — `gateway/appsettings.json`

The gateway is configured by file, not `.env`. Key values:

| Setting | Value | Notes |
| --- | --- | --- |
| `Kestrel:Endpoints:Http:Url` | `http://localhost:5080` | `localhost` (not `127.0.0.1`) so Kestrel binds both loopbacks. **Ports 5000 and 7000 are taken by macOS AirPlay Receiver** — hence 5080. |
| `ReverseProxy:Clusters:go:Destinations:primary:Address` | `http://127.0.0.1:8000/` | Must match the agent's `PORT`. |
| `ReverseProxy:Clusters:go:HttpRequest:ActivityTimeout` | `00:10:00` | Long enough that a live WebSocket is not torn down mid-stream. |

Standard ASP.NET Core environment variables still apply and override the file, using `__` as the section separator:

| Variable | Example | Effect |
| --- | --- | --- |
| `ASPNETCORE_ENVIRONMENT` | `Development` | Set by `Properties/launchSettings.json`. **Gates the CORS policy** — the browser-facing CORS allowances exist only in Development. |
| `ASPNETCORE_URLS` | `http://localhost:5080` | Overrides the Kestrel binding. |
| `ReverseProxy__Clusters__go__Destinations__primary__Address` | `http://127.0.0.1:9000/` | Repoints the proxy without editing the file. |

The gateway handles WebSocket upgrades through YARP's `IHttpUpgradeFeature`, so `app.UseWebSockets()` is deliberately absent — that middleware is for *terminating* sockets, which the gateway never does.

### Client — `client/.env`

**Empty, and nothing reads it yet.** `client/.env` exists as a placeholder; there are no `import.meta.env` references anywhere in `client/src`. When the dashboard is wired up, Vite requires the `VITE_` prefix for a variable to reach browser code — e.g. `VITE_API_BASE=http://localhost:5080`.

Note that `vite.config.ts` currently defines **no dev proxy**, so the browser will call the gateway cross-origin on `:5080` and depend on its Development CORS policy. Adding a Vite proxy instead would make every request reach the gateway server-side, where CORS does not apply.

> `.env` and `.env.*` are gitignored repo-wide (except `.env.example`, which does not currently exist).

## Development

```bash
# server/
go build ./...
go vet ./...
go test -race ./...     # tests live in internal/core and internal/ws

# client/
bun run dev
bun run build           # tsc -b && vite build
bun run lint

# gateway/
dotnet build
dotnet run
```

Current test coverage is `internal/core` (config parsing) and `internal/ws` (hub concurrency). The collectors in `internal/agent/network` have no tests — they read live machine state.

The `-race` flag matters more than usual here. `Hub.clients` and `Hub.history` are owned exclusively by `Run`'s goroutine and are deliberately **unsynchronized**; the only cross-goroutine read is `ClientCount()`, served by an `atomic.Int64`. Do not add a mutex back. If you add a method needing those fields, route it through a channel into `Run`'s select, or extend the atomic pattern — `go test -race ./...` is what enforces this.

`go build` prints `ld: warning: ignoring duplicate libraries: '-lpcap'` on macOS. It is harmless — cgo passes `-lpcap` twice while linking gopacket against the system libpcap.

### Adding a route

Each `Register*Routes` function in `internal/agent/api/routes/` owns its own subgroup, and `main.go` mounts them under `API_PREFIX`. A full path is `API_PREFIX + group + route`. Write the registrar, add one call in `main.go`, and if it needs config, introduce a `Deps` struct rather than another positional parameter — `RegisterNetworkRoutes(rg)` and `RegisterWSRoutes(rg, hub, origins)` already disagree on shape.

### Adding a collector

Collectors live in `internal/agent/network` and stay free of `gin` — the samplers call the same functions and have no HTTP request to fail. Return a plain error the sampler can turn into an error frame. Four rules, each corresponding to a real failure mode:

1. **A `Source` must return promptly.** The sampler loop is shared by every connected client. Anything that captures continuously buffers in its own goroutine, with the `Source` draining that buffer rather than capturing synchronously.
2. **Sources return a delta, not a cumulative total** — for anything rate-shaped. The sampler broadcasts whatever comes back, once per tick.
3. **Guard every process lookup.** Processes routinely exit between reading the connection table and looking up the PID. Report the connection with an unknown app name; do not drop it or fail.
4. **Check `Raddr.IP` before using it.** Listening sockets have no remote peer and report an empty `IP` string.

And return an **error**, not an empty result, when a source cannot read — "no data" and "cannot read" are different states, and showing an empty list for both is indistinguishable from a healthy idle machine.

### Storage

There is **no database**, by design. Recent frames live in the hub's bounded in-memory buffer (300 frames per stream type) with automatic eviction and capped memory. History does not survive a restart; that is the accepted tradeoff. Don't add an ORM or migration tool to satisfy a short-term need.

## Troubleshooting

| Symptom | Cause |
| --- | --- |
| `listen tcp 127.0.0.1:8000: bind: address already in use` | An instance is already running. `lsof -nP -iTCP:8000 -sTCP:LISTEN` |
| WebSocket handshake returns **403** | Origin not in `CORS_ORIGINS`. Through the gateway, check `http://gateway.internal` is listed. |
| `packets` stream sends an `error` frame, `snapshot` works | `/dev/bpf*` not accessible — see [Privileges](#privileges). Everything else is unaffected. |
| websocat: `No URL specified` | Use `-H='Origin: ...'` with the equals sign. |
| Gateway 502 | Agent is not running. Start it first. |
| Throughput reads 0, or `elapsedMs` is tiny | First call after startup has nothing to diff against; small values are the HTTP endpoint sharing sampler state — read the `snapshot` stream instead. |
| Hostnames stay `""` | Normal — most remote IPs have no PTR record, and negative results are cached for 10 minutes. |
| Builds or servers hang at ~0% CPU | Check `df -h`. This machine's disk has run near capacity, and process startup and file I/O stall unpredictably when it is that full. |

## History

The backend was ported from Python/FastAPI. That implementation is preserved in git at `e793086`; it is not on disk. The `sudo` requirement, the `{"data": ...}` envelope, and the connection JSON shape all date from it.
