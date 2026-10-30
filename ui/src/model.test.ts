import {describe, expect, it} from 'vitest';

import {connState, daysUntil, formatBytes, formatExpiry, looksLikeURL, parseUsers, pathOf, tailnetLink, type Status} from './model.js';

const t = (k: string, p?: Record<string, string | number>) => k.replace(/\{(\w+)\}/g, (_, n) => String(p?.[n]));

function st(over: Partial<Status>): Status {
    return {running: true, state: 'Running', peers: [], https: false, connections: 0, forwarded: 0, refused: 0, control_url: '', has_identity: true, enabled: true, consented: true, now: 0, ...over};
}

describe('connState', () => {
    it('maps the backend state', () => {
        expect(connState(undefined)).toBe('off');
        expect(connState(st({enabled: false}))).toBe('off');
        expect(connState(st({running: false, error: 'boom'}))).toBe('error');
        expect(connState(st({state: 'NeedsLogin'}))).toBe('login');
        expect(connState(st({state: 'NeedsMachineAuth'}))).toBe('approval');
        expect(connState(st({state: 'Starting'}))).toBe('starting');
        expect(connState(st({self: {name: 'ccu', dns_name: '', addresses: [], online: true}}))).toBe('connected');
        expect(connState(st({self: {name: 'ccu', dns_name: '', addresses: [], online: false}}))).toBe('offline');
        expect(connState(st({self: {name: 'ccu', dns_name: '', addresses: [], online: true, expired: true}}))).toBe('expired');
    });
});

describe('helpers', () => {
    it('parses the allow-list', () => {
        expect(parseUsers(' alice@example.com\n\ntag:phone, alice@example.com ;bob ')).toEqual(['alice@example.com', 'tag:phone', 'bob']);
    });
    it('formats bytes', () => {
        expect(formatBytes(12)).toBe('12 B');
        expect(formatBytes(1234)).toBe('1.2 kB');
        expect(formatBytes(123_456_789)).toBe('123 MB');
    });
    it('formats the expiry', () => {
        const day = 86_400_000;
        expect(formatExpiry(0, 0, t)).toBe('never');
        expect(formatExpiry(10 * day + 1, 0, t)).toBe('in 11 days');
        expect(formatExpiry(day, 0, t)).toBe('tomorrow');
        expect(formatExpiry(1000, 0, t)).toBe('tomorrow');
        expect(formatExpiry(0.5 * day, 0.6 * day, t)).toBe('today');
        expect(formatExpiry(0, 3 * day, t)).toBe('never');
        expect(formatExpiry(1, 3 * day + 1, t)).toBe('expired 3 days ago');
        expect(daysUntil(0, 0)).toBe(0);
    });
    it('builds the tailnet link', () => {
        expect(tailnetLink(undefined)).toBe('');
        expect(tailnetLink(st({self: {name: 'ccu', dns_name: 'ccu.tail.test', addresses: ['100.64.0.1'], online: true}}))).toBe('http://ccu.tail.test/');
        expect(tailnetLink(st({https: true, self: {name: 'ccu', dns_name: 'ccu.tail.test', cert_domain: 'ccu.tail1234.ts.net', addresses: [], online: true}}))).toBe('https://ccu.tail1234.ts.net/');
        expect(tailnetLink(st({self: {name: 'ccu', dns_name: '', addresses: ['fd7a:115c:a1e0::1'], online: true}}))).toBe('http://[fd7a:115c:a1e0::1]/');
    });
    it('splits the path', () => {
        expect(pathOf('direct 203.0.113.5:41641')).toEqual({kind: 'direct', detail: '203.0.113.5:41641'});
        expect(pathOf('relay fra')).toEqual({kind: 'relay', detail: 'fra'});
        expect(pathOf(undefined).kind).toBe('none');
    });
    it('checks a login server URL', () => {
        expect(looksLikeURL('https://headscale.example.org')).toBe(true);
        expect(looksLikeURL('http://10.0.0.5:8080/hs')).toBe(true);
        expect(looksLikeURL('headscale.example.org')).toBe(false);
    });
});
