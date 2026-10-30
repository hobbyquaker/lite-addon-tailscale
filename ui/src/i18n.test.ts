import {readdirSync, readFileSync} from 'node:fs';
import {describe, expect, it} from 'vitest';

import {MESSAGES} from './messages.js';

// every t('…') of the page has its German and English text
describe('messages', () => {
    it('covers every key', () => {
        const dir = process.cwd() + '/src/';
        const missing: string[] = [];
        for (const f of readdirSync(dir).filter((f) => f.endsWith('.svelte') || f === 'model.ts')) {
            const src = readFileSync(dir + f, 'utf8');
            for (const m of src.matchAll(/\bt\((?:'((?:[^'\\]|\\.)*)'|"((?:[^"\\]|\\.)*)")/g)) {
                const key = (m[1] ?? m[2]!).replace(/\\'/g, "'");
                if (key.endsWith('.') || /\.\s*\+/.test(key)) continue;
                const e = MESSAGES[key] as {de?: string; en?: string} | undefined;
                if (!e?.de || !e?.en) missing.push(`${f}: ${key}`);
            }
        }
        for (const k of ['off', 'starting', 'login', 'approval', 'connected', 'offline', 'expired', 'error']) if (!MESSAGES['conn.' + k]) missing.push('conn.' + k);
        for (const k of ['online', 'offline', 'expired']) if (!MESSAGES['peer.' + k]) missing.push('peer.' + k);
        for (let i = 1; i <= 5; i++) if (!MESSAGES['howto.' + i]) missing.push('howto.' + i);
        for (const k of ['1', 'learns', 'not', 'reach', 'ct']) if (!MESSAGES['consent.' + k]) missing.push('consent.' + k);
        // the state hints are looked up by their English text (the hints map in ConnectionTab)
        const tab = readFileSync(dir + 'ConnectionTab.svelte', 'utf8');
        for (const m of tab.matchAll(/^\s+\w+: ['"](.+?)['"],$/gm)) if (!MESSAGES[m[1]!]) missing.push('hint: ' + m[1]);
        expect(missing).toEqual([]);
    });
    it('never says "box"', () => {
        for (const [k, v] of Object.entries(MESSAGES)) {
            const e = v as {de: string; en: string};
            // FRITZ!Box is a product name, not the system
            expect(`${k} ${e.de} ${e.en}`.replace(/FRITZ!Box/g, 'FRITZ')).not.toMatch(/\bbox\b/i);
        }
    });
});
