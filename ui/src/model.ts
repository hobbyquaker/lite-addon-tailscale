// The API's shapes and the page's pure helpers (tested in model.test.ts).

export interface Config {
    consented: boolean;
    enabled: boolean;
    control_url: string;
    hostname: string;
    https: boolean;
    redirect: boolean;
    who: 'all' | 'users';
    allowed_users: string[];
    ranges: string[];
    client_version: string;
    version: string;
}

export interface Settings {
    control_url: string;
    hostname: string;
    https: boolean;
    redirect: boolean;
    who: 'all' | 'users';
    allowed_users: string[];
}

export interface Peer {
    name: string;
    dns_name: string;
    os: string;
    user?: string;
    tags?: string[];
    addresses: string[];
    online: boolean;
    last_seen?: number;
    last_handshake?: number;
    rx_bytes: number;
    tx_bytes: number;
    path?: string;
    allowed: boolean;
    connections: number;
    expired?: boolean;
}

export interface Self {
    name: string;
    dns_name: string;
    addresses: string[];
    user?: string;
    tags?: string[];
    key_expiry?: number;
    expired?: boolean;
    tailnet?: string;
    magic_dns?: string;
    cert_domain?: string;
    relay?: string;
    online: boolean;
}

export interface Status {
    running: boolean;
    state: string;
    login_url?: string;
    login_qr?: string;
    error?: string;
    health?: string[];
    self?: Self;
    peers: Peer[];
    https: boolean;
    https_error?: string;
    connections: number;
    forwarded: number;
    refused: number;
    socket_error?: string;
    version?: string;
    since?: number;
    control_url: string;
    has_identity: boolean;
    enabled: boolean;
    consented: boolean;
    now: number;
}

export interface Facts {
    checked: boolean;
    token_ok: boolean;
    scopes: string[];
    error?: string;
    backup_unencrypted: boolean;
    hostname?: string;
    domain?: string;
    addresses: string[];
    at: number;
}

export type ConnState = 'off' | 'starting' | 'login' | 'approval' | 'connected' | 'offline' | 'expired' | 'error';

/** The connection's state for its pill, from the node's backend state and the switch. */
export function connState(st: Status | undefined): ConnState {
    if (!st || !st.enabled || !st.consented) return 'off';
    if (!st.running) return st.error ? 'error' : 'off';
    switch (st.state) {
        case 'NeedsLogin':
            return 'login';
        case 'NeedsMachineAuth':
            return 'approval';
        case 'Running':
            if (st.self?.expired) return 'expired';
            return st.self && !st.self.online ? 'offline' : 'connected';
        default:
            return st.error ? 'error' : 'starting';
    }
}

export function settingsOf(c: Config): Settings {
    return {control_url: c.control_url, hostname: c.hostname, https: c.https, redirect: c.redirect, who: c.who, allowed_users: [...c.allowed_users]};
}

/** The allow-list as the textarea shows it: one entry per line. */
export function usersText(users: string[]): string {
    return users.join('\n');
}

/** The textarea back into entries: trimmed, empty lines and duplicates dropped. */
export function parseUsers(text: string): string[] {
    const out: string[] = [];
    for (const raw of text.split(/[\n,;]+/)) {
        const u = raw.trim();
        if (u && !out.includes(u)) out.push(u);
    }
    return out;
}

/** 1.2 MB, 340 kB, 12 B - bytes with a unit, one decimal above a thousand. */
export function formatBytes(n: number): string {
    if (!Number.isFinite(n) || n < 0) return '–';
    const units = ['B', 'kB', 'MB', 'GB', 'TB'];
    let v = n;
    let i = 0;
    while (v >= 1000 && i < units.length - 1) {
        v /= 1000;
        i++;
    }
    return (i === 0 ? String(Math.round(v)) : v.toFixed(v >= 100 ? 0 : 1)) + ' ' + units[i];
}

/** The age of a moment as a short phrase; t translates the templates. */
export function formatAge(at: number | undefined, now: number, t: (k: string, p?: Record<string, string | number>) => string): string {
    if (!at) return t('never');
    const s = Math.max(0, Math.round((now - at) / 1000));
    if (s < 60) return t('{n} s ago', {n: s});
    if (s < 3600) return t('{n} min ago', {n: Math.floor(s / 60)});
    if (s < 86400) return t('{n} h ago', {n: Math.floor(s / 3600)});
    return t('{n} d ago', {n: Math.floor(s / 86400)});
}

/** Days until a moment (negative when past), rounded up. */
export function daysUntil(at: number, now: number): number {
    return Math.ceil((at - now) / 86_400_000);
}

/** The key expiry as a phrase: never, in N days, today, expired N days ago. */
export function formatExpiry(at: number | undefined, now: number, t: (k: string, p?: Record<string, string | number>) => string): string {
    if (!at) return t('never');
    const d = daysUntil(at, now);
    if (d > 1) return t('in {n} days', {n: d});
    if (d === 1) return t('tomorrow');
    if (d === 0) return t('today');
    return t('expired {n} days ago', {n: -d});
}

/** A local date for a unix millisecond moment. */
export function formatDate(at: number, lang: string): string {
    return new Date(at).toLocaleDateString(lang === 'de' ? 'de-DE' : 'en-GB', {year: 'numeric', month: '2-digit', day: '2-digit'});
}

/** The link to this system on the tailnet: https with the certificate name, else http on the MagicDNS name or the first address. */
export function tailnetLink(st: Status | undefined): string {
    if (!st?.self) return '';
    if (st.https && st.self.cert_domain) return `https://${st.self.cert_domain}/`;
    if (st.self.dns_name) return `http://${st.self.dns_name}/`;
    const a = st.self.addresses[0];
    return a ? `http://${a.includes(':') ? `[${a}]` : a}/` : '';
}

/** "direct 203.0.113.5:41641" → {kind: 'direct', detail: '203.0.113.5:41641'}. */
export function pathOf(p: string | undefined): {kind: 'direct' | 'relay' | 'none'; detail: string} {
    if (!p) return {kind: 'none', detail: ''};
    const [kind, ...rest] = p.split(' ');
    return {kind: kind === 'direct' ? 'direct' : 'relay', detail: rest.join(' ')};
}

/** A plausible Headscale URL: http(s) and a host. */
export function looksLikeURL(s: string): boolean {
    return /^https?:\/\/[A-Za-z0-9.\-]+(:[0-9]{1,5})?(\/[^\s]*)?$/.test(s.trim());
}
