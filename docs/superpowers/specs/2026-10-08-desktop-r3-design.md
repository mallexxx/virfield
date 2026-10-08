# R3: Desktop access to VM leases

Status: approved design (2026-10-08). Follows R2
(`2026-10-08-hackintosh-nodes-r2-design.md`).

## Goal

Open the desktop of a ready VM lease (Tart on this Mac, or a macOS VM on the
HTPC) in Screen Sharing.app with one command or one click, the way Lume's
`vncUrl` worked in v2. Target ports stay private: nothing new listens on the
LAN.

Out of scope: bare-metal node leases (the MacBook), a browser noVNC view (a
possible later step over the same tunnel), and pre-boot or Recovery screens.

## Decisions

- **Source: guest macOS Screen Sharing.** Each VM lease enables it during
  lease preparation, so existing goldens need no rebuild. Clients sign in as
  `admin` with the lease's own password, which Virfield already generates and
  stores per lease. Runtime VNC was rejected: Tart's `--vnc-experimental`
  listens on every interface of this Mac behind a weak VNC password, and
  QEMU's VNC has no password.
- **Endpoints.**
  - Tart: `<lease IP>:5900` on Tart's host-only network.
  - HTPC VM: the guest's 5900 is forwarded by passt (`USER_PORTS=22,5900`,
    QEMU's own VNC moved off 5900) and published on the node loopback at the
    lease SSH port + 1000 (22100 → 23100). Virfield reaches it through the
    pinned node SSH connection, like guest SSH.
- **Access: the existing lease tunnel, per service.** `OpenTunnel` gains a
  service, `ssh` (default, unchanged) or `desktop`, with at most one tunnel per
  lease and service on `127.0.0.1:<random>` of the daemon host. Tunnels keep
  their lifetime rules: closed on release, expiry, drift or explicit close.
- **Credential delivery.** The `desktop` tunnel response includes
  `url: vnc://admin:<lease password>@127.0.0.1:<port>`. Only an authenticated
  API caller receives it; the password is the lease's guest password, which a
  lease owner may already use for sudo.

## Components

| Unit | Change |
|---|---|
| `internal/guestssh` | `leaseScript` enables Screen Sharing (`launchctl enable system/com.apple.screensharing`, then `kickstart -k`); `Prepare` verifies the guest listens on 5900. `Manager.LeasePassword(l)` reads the lease credential. |
| `internal/baremetal` | VM containers publish `127.0.0.1:<ssh port+1000>:5900` with `USER_PORTS=22,5900` and QEMU VNC on another port; `DialService(ctx, l, "desktop")` tunnels through the node to that port. Config validation reserves the desktop range. |
| `internal/provider`, `internal/fleet` | `ServiceDialer` interface `DialService(ctx, lease, service) (net.Conn, error)`; the fleet routes by lease provider. The primary runtime's default dials `lease.IP:5900` for desktop. |
| `internal/control/tunnel.go` | Tunnels keyed by lease and service; desktop tunnels dial through `ServiceDialer` and return the `vnc://` URL from the preparer's lease password. |
| `internal/httpapi` | `POST /api/v1/leases/{id}/tunnel` accepts an optional `{"service":"desktop"}` body; `DELETE` accepts `?service=desktop`. |
| `cmd/virfield` | `virfield desktop LEASE_ID [--print]` opens the tunnel and runs `open <url>`. |
| `internal/mcpadapter` | `vm_tunnel` and `vm_tunnel_close` take an optional `service`. |
| `internal/web/app.js` | A "Desktop" button on ready VM leases opens the returned URL. |

## Errors

- Desktop on a host lease or a non-ready lease: `lease_unavailable`.
- Guest not listening on 5900 at preparation: `lease_ssh_failed` (the lease
  never becomes ready), with the stage log kept privately.
- Node unreachable at connection time: the tunnel accepts and closes the
  client connection, as SSH tunnels do today.

## Testing

- Unit: lease script contains the Screen Sharing enablement and Prepare
  rejects a guest without 5900; tunnel service routing and per-service
  lifetime; node desktop dial through a forwarding test server; container
  arguments; HTTP body and query handling; CLI `desktop --print`.
- Live: one Tart lease and one HTPC lease: the desktop tunnel returns Apple
  RFB (`RFB 003.889`), Screen Sharing signs in with the lease password
  (screenshot), and the tunnel is closed after release.
