# The page's API

The daemon serves the page and its API at `/addons/tailscale/` (lighttpd proxies it to `127.0.0.1:8299`). Every
request carries the system's session; only administrators are admitted (an API token of the scope `addon:tailscale`
or Full access passes the system's gate as well). Every write (`POST`, `PUT`) needs the header `X-CCU-Addon-Request`.
Errors are `{"error": "<code>", "message": "…"}`: `invalid` (400 for bad JSON, 422 for a refused value), `not-running`
(409: the node must be connected first), `login` / `logout` (502: the login server refused), `not-found` (404).

No secret is ever answered: an auth key is used for the registration and never stored; the node's keys stay in its
state directory. The login URL is answered to the page while a login is open (it is a capability for a few minutes)
and never written to the journal.

| Route | What |
| --- | --- |
| `GET api/v1/health` | `{ok, version, uptime_s, ready}`; with `?ready=1` 503 while the node should run but does not |
| `GET api/v1/config` | `{consented, enabled, control_url, hostname, https, redirect, who: all\|users, allowed_users: [...], ranges: ["100.64.0.0/10", "fd7a:115c:a1e0::/48"], client_version, version}` - `control_url` `""` is Tailscale's login server; `ranges` are the manifest's ingress ranges; `client_version` the embedded Tailscale client |
| `PUT api/v1/settings` | `{control_url, hostname, https, redirect, who, allowed_users}` - the hostname is a DNS label (set on the running node at once); `https` off/on restarts the node; `control_url` may only change while the node has no identity (422 otherwise: log out and forget first); `allowed_users` are login names (`alice@example.com`, on a Headscale the user name) and tags (`tag:phone`), at least one when `who` is `users`. Answers the configuration. |
| `POST api/v1/connect` | `{control_url?, auth_key?}` - records the consent (the first time; the consent card's login server goes with it), switches the node on and starts it; with `auth_key` the node registers at once (the key is used once and never stored), without one it asks for an interactive login (`status.login_url`). Answers the status. |
| `POST api/v1/disconnect` | switches the node off and stops it; the identity stays. Answers the status. |
| `POST api/v1/login` | `{auth_key?}` - a new interactive login (a fresh `login_url`), or with `auth_key` the registration with that key. 409 while the node is not running. Answers the status. |
| `POST api/v1/logout` | `{forget?}` - the login server expires the node's key and the node asks for a login again; with `forget` the node is also stopped, its state directory emptied (keys, identity, certificates) and the addon switched off (the consent stays). Answers the status. |
| `GET api/v1/status` | `{running, state, login_url?, login_qr?, error?, health?: [...], self?: {name, dns_name, addresses, user?, tags?, key_expiry?, expired?, tailnet?, magic_dns?, cert_domain?, relay?, online}, peers: [{name, dns_name, os, user?, tags?, addresses, online, last_seen?, last_handshake?, rx_bytes, tx_bytes, path?, allowed, connections, expired?}], https, https_error?, connections, forwarded, refused, socket_error?, version, since?, control_url, has_identity, enabled, consented, now}` - `state` is Tailscale's backend state (`NoState`, `NeedsLogin`, `NeedsMachineAuth`, `Starting`, `Running`, `Stopped`); `login_qr` a PNG data URL of `login_url`; `key_expiry` unix milliseconds (absent: never); `path` `"direct <endpoint>"` or `"relay <region>"`; `allowed` whether the who-may-connect rule admits the peer; `https` whether 443 is served with the tailnet's certificate, `https_error` `no-certificate` or the listener's error when not; `refused` the connections turned away (outside the rule, or no tailnet identity); `has_identity` whether the node holds a key for its login server |
| `GET api/v1/system` | what the system says, with the addon's token and cached for 15 s (`?refresh=1` reads anew): `{checked, token_ok, scopes, error?, backup_unencrypted, hostname?, domain?, addresses, at}` - `backup_unencrypted` from the system's warnings (the node's keys are in the backup) |

## What the daemon calls

- **The system:** `GET /api/auth/v1/state` with its token (the scopes), `GET /api/system/v1/warnings`
  (`backup-unencrypted`) and `GET /api/system/v1/network` (the system's name and addresses) - all `system:read`, all
  read-only, for the page's banners.
- **The web server**, through its ingress socket `/run/lighttpd-addons/tailscale/proxy.sock`: every TCP connection a
  tailnet device opens to the node's address on port 80 or 443 is forwarded there with a PROXY protocol v2 header that
  names the device's tailnet address - and, for 443, the SSL TLV (`PP2_TYPE_SSL` with `PP2_CLIENT_SSL` and the TLS
  version), so the web server treats the request as `https`. A device the who-may-connect rule refuses is closed before
  anything is forwarded.
- **The login server and its relays** - only after the user's consent on the page (`POST api/v1/connect`), then
  continuously: `controlplane.tailscale.com` / `login.tailscale.com` and the DERP relays (`*.tailscale.com`, HTTPS 443,
  STUN UDP 3478) by default, or the configured Headscale URL and the relays it names. Nothing else: no log upload, no
  port mapping (compiled out), no update check.
