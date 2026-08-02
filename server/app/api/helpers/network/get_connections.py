import psutil


class ConnectionsUnavailable(Exception):
    """Raised when the system-wide connection table cannot be read."""


def get_connections():
    """Return the current inet connections as JSON-serializable dicts.

    Raises ConnectionsUnavailable when psutil cannot read the system-wide
    connection table, which on macOS means the process is not running with
    elevated privileges.
    """
    try:
        conns = psutil.net_connections(kind="inet")
    except psutil.AccessDenied as exc:
        raise ConnectionsUnavailable from exc

    connections = []
    for conn in conns:
        if conn.raddr and conn.status == "ESTABLISHED":
            connections.append({
                "laddr": {"ip": conn.laddr.ip, "port": conn.laddr.port} if conn.laddr else None,
                "raddr": {"ip": conn.raddr.ip, "port": conn.raddr.port} if conn.raddr else None,
                "status": conn.status,
                "pid": conn.pid,
            })
    return connections
