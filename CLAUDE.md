# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

A personal network monitoring tool. It watches **this machine's own** network activity and streams it to a browser dashboard in real time. Single-user, local-only — there is no multi-tenancy and no remote agent.

The backend samples the system on a fixed interval from a background asyncio task and pushes snapshots to the frontend over a WebSocket. The frontend consumes that stream and draws live charts.

Four things are monitored:

- **Per-connection live view** — remote host, port, owning application, connection state.
- **Throughput over time** — derived by diffing consecutive `net_io_counters()` readings.
- **Per-application rollups** — connections and traffic grouped by owning process.
- **Remote-endpoint enrichment** — hostname and geo for remote IPs.

## Project state

Early scaffold. The FastAPI app boots and serves placeholder routes returning hardcoded literals, but **none of the monitoring described above is implemented yet**. `server/app/models/`, `schemas/`, `db/`, `bin/`, `api/helpers/user/`, `server/tests/`, and both `infra/*.tf` files are empty placeholders. Treat empty directories as intent — they mark where a layer belongs.

`user_routes.py` is leftover boilerplate returning fake users; it is not part of the product.

## Commands

Backend (run from `server/` — see import-root note below):

```bash
sudo .venv/bin/uvicorn app.main:app --reload   # see privileges section
.venv/bin/pip install -r reqirments.txt        # note: filename is misspelled in-repo
```

Frontend (from `client/`):

```bash
bun install
bun run dev        # Vite dev server on :5173
bun run build      # tsc -b && vite build
bun run lint
```

No test runner is configured in either project yet. Bun is the client package manager (`bun.lock`, no npm or yarn lockfile).

## Privileges — read before debugging "empty connection list"

Verified on this machine (macOS, psutil 7.2.2): **`psutil.net_connections()` raises `AccessDenied` without elevated privileges.** Not a partial result — it throws.

The split matters, because the two data sources degrade differently:

| Call | Unprivileged | Notes |
| --- | --- | --- |
| `psutil.net_io_counters()` | works | throughput charts function without sudo |
| `psutil.Process().net_connections()` | works | own process only |
| `psutil.net_connections()` | **AccessDenied** | system-wide listing; needs sudo on macOS/Windows |

So an empty or failed connection list with working throughput graphs is almost certainly a privilege problem, not a bug in the sampling code. Run the backend under `sudo` for the connection features. The connection collector should catch `AccessDenied` at the top level and surface a clear "needs elevated privileges" state to the dashboard rather than presenting an empty list, which is indistinguishable from "no active connections."

## Data collection conventions

These are correctness requirements, not style preferences. Each one corresponds to a real failure mode.

**Throughput is a difference, never a reading.** `net_io_counters()` returns cumulative counters since boot. A single sample carries no rate information. Always retain the previous reading plus its timestamp and compute the delta over the elapsed interval. Use the measured elapsed time, not the nominal interval — the loop will drift under load, and dividing by the nominal value silently skews every rate.

**Guard every `psutil.Process(pid)` lookup** against `NoSuchProcess` and `AccessDenied`. Processes routinely exit between the moment `net_connections()` lists them and the moment you look up the owning process — this is a normal race, not an exceptional case, and it will crash the sampling loop if unhandled. A connection whose process has vanished should still be reported, with the app name left unknown.

**Check `conn.raddr` before using it.** Listening sockets have no remote address; `raddr` is an empty tuple. Unconditional access to `conn.raddr.ip` will crash on the first listening socket, and there is always at least one.

**Keep DNS resolution off the sampling loop.** `socket.gethostbyaddr()` is slow and blocking — a handful of unresolvable IPs will stall a fixed-interval sampler well past its deadline and stall the WebSocket stream with it. Resolve out of band and cache results, keyed by IP. Emit snapshots with raw IPs immediately and let hostnames arrive in a later frame rather than blocking a snapshot on enrichment. Cache negative results too; unresolvable addresses are common and retrying them every tick is the expensive path.

**scapy is deliberately not used yet.** It is reserved for optional packet-level inspection later. Everything in v1 comes from psutil. Don't reach for scapy to solve a problem psutil already covers — it raises the privilege requirements further and is not currently a dependency.

## Storage

**No database in v1.** Recent history lives in an in-memory `collections.deque` used as a ring buffer with a bounded `maxlen`, so eviction is automatic and memory is capped. History does not survive a restart, and that is the accepted tradeoff for now.

SQLite is planned for persistence later. The empty `db/` and `models/` directories are reserved for it. Don't add an ORM or a migration tool to satisfy a short-term need — a deque is the current design, not a placeholder for one.

## Backend architecture

The app lives in `server/app/` and is importable as the `app` package. Every module is reached through absolute imports rooted there (`from app.core.config import settings`). **Run commands from `server/`** so that directory is on `sys.path`; running from anywhere else breaks every import.

Routers live in `app/api/routes/`, and each owns its `prefix` and `tags` on the `APIRouter(...)` constructor rather than repeating the path segment on every decorator. `main.py` mounts them under `settings.api_prefix`, so a full path is `api_prefix + router prefix + decorator path`. Add a router by creating the module and adding one `include_router` call.

Configuration is centralized in `app/core/config.py` as a `pydantic-settings` `Settings` model exported as a module-level `settings` singleton, populated from `server/.env` with case-insensitive field matching. `cors_origins` is typed `list[str]` and must be **JSON-formatted** in `.env` (`["http://localhost:5173"]`) — comma-separated values will not parse. CORS is preconfigured for Vite's `:5173`.

The sampling task should be owned by the app lifespan, not started per-connection — it samples the machine, so it runs once regardless of how many dashboard clients are attached, and WebSocket handlers subscribe to its output. Sampling that stops when the last client disconnects loses history the ring buffer is meant to retain.

### Verifying routes

`app.routes` does **not** flatten included routers in this FastAPI version — mounted routers appear as opaque `_IncludedRouter` entries with no `.path`, so iterating it makes correctly-registered endpoints look missing. Verify with real requests against a running server. `fastapi.testclient.TestClient` is unavailable unless `httpx2` is installed, which is not in the requirements.

## Environment notes

- Python 3.11 in `server/.venv/`. FastAPI is also installed globally but `pydantic-settings` and `psutil` are not — always use the venv binaries, never bare `python3`.
- `psutil` is installed in the venv but **missing from `reqirments.txt`**; add it there when the collector lands. `scapy` is not installed.
- `server/` has no `.gitignore`, so `.venv/` and `.env` are untracked but unignored. Worth adding before committing.
