# Changelog

## [0.1.0] - 2026-10-30

The first release, for openccu-lite 1.1.0-dev.6 and newer (the web ingress of the system).

- **Tailscale in userspace** (`tsnet`, Tailscale's own library): the tailnet node lives inside the addon's process
  with its own network stack - no TUN device, no capability, no root, no open port and no port forward. It works
  behind DS-Lite and CGNAT; direct paths form where the NAT allows, else the traffic goes end-to-end encrypted through
  a relay.
- **Only this system is reachable:** the node listens on its tailnet address for the web (80, and 443 with the
  tailnet's certificate where there is one) and hands every connection to the system's web ingress with the peer's
  tailnet address in front (PROXY protocol; the SSL TLV for 443, so the system treats the request as HTTPS). The
  login lockout, API token address ranges and the journal see `100.x`, not the loopback. No exit node, no subnet
  routes, no Funnel, no Tailscale SSH.
- **Nothing before consent:** the page shows what will be contacted and what the login server learns before the
  first *Connect*; until then the addon contacts nothing.
- **Login** with the Tailscale login link - shown as a button and as a QR code for the phone - or with an auth key
  (used once, never stored); re-login; log out; *log out and forget this device*. The page shows the node's MagicDNS
  name as a link, its addresses, the user, the key expiry (with a warning from 14 days before and when expired), the
  Tailscale client version, the relay region and the connections.
- **Headscale:** a login server field lets the node register with a self-hosted Headscale instead of Tailscale Inc.
- **HTTPS on the tailnet** with the certificate Tailscale issues for the node (MagicDNS and HTTPS on in the admin
  console), port 80 redirecting; plain HTTP where there is none (Headscale). Both switchable.
- **Who may connect:** every device the tailnet policy lets through, or only devices of listed users or with listed
  tags - checked per connection with the tailnet's own record (WhoIs); others are refused before they reach the web
  server. The *Devices* tab lists the tailnet's devices with state, path (direct or relay), traffic and whether each
  may connect.
- **Privacy:** log upload to Tailscale, UPnP/NAT-PMP, control-plane debug access, port-list and metrics reporting,
  self-update, Taildrop, Drive, the web client and SSH are compiled out of the binary. The node's state (its keys)
  lives in the configuration directory and is in the system's backup; the page says so when the backups are
  unencrypted.
- German and English, light and dark. A Headscale-in-docker integration test; a release-following workflow that bumps
  the Tailscale version when Tailscale releases.
