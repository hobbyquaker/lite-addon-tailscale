<script lang="ts">
    // The settings: the hostname, HTTPS and the redirect, the login server (only while logged
    // out), who may connect.
    import {untrack} from 'svelte';
    import {t} from 'occulite-ui/i18n';
    import type {NoticesStore} from 'occulite-ui/notices';

    import {put} from './api.js';
    import {looksLikeURL, parseUsers, settingsOf, usersText, type Config, type Settings, type Status} from './model.js';

    let {config, status, notices, onchanged}: {config: Config; status: Status | undefined; notices: NoticesStore; onchanged: (c: Config) => void; onstatus: (s: Status) => void} = $props();

    let form = $state<Settings>(untrack(() => settingsOf(config)));
    let users = $state(untrack(() => usersText(config.allowed_users)));
    let server = $state<'tailscale' | 'headscale'>(untrack(() => (config.control_url ? 'headscale' : 'tailscale')));
    let busy = $state(false);

    $effect(() => {
        form = settingsOf(config);
        users = usersText(config.allowed_users);
        server = config.control_url ? 'headscale' : 'tailscale';
    });

    const locked = $derived(!!status?.has_identity);
    const payload = $derived<Settings>({...form, control_url: server === 'tailscale' ? '' : form.control_url.trim(), allowed_users: parseUsers(users)});
    const dirty = $derived(JSON.stringify(payload) !== JSON.stringify(settingsOf(config)));
    const valid = $derived(/^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/.test(form.hostname.trim()) && (server === 'tailscale' || looksLikeURL(form.control_url)) && (form.who === 'all' || parseUsers(users).length > 0));

    async function save() {
        busy = true;
        try {
            onchanged(await put<Config>('api/v1/settings', payload));
            notices.push('info', t('Saved.'));
        } catch (e) {
            notices.push('error', (e as Error).message);
        } finally {
            busy = false;
        }
    }
</script>

<section class="ts-section" data-testid="settings">
    <h2>{t('Settings')}</h2>
    <div class="ts-form">
        <label for="st-host">{t('Hostname')}</label>
        <input class="hmm-input" id="st-host" type="text" bind:value={form.hostname} maxlength="63" spellcheck="false" autocomplete="off" data-testid="st-hostname" />
        <p class="ts-help">{t('The name of this system on the tailnet: lower-case letters, digits, hyphens. It becomes the MagicDNS name (ccu.<tailnet>.ts.net). With HTTPS on, the certificate puts that name into the public Certificate Transparency logs - keep it neutral.')}</p>
        <label class="ts-check"><input type="checkbox" bind:checked={form.https} data-testid="st-https" /> {t('HTTPS on the tailnet (443) with the certificate the tailnet issues for this node')}</label>
        <p class="ts-help">{t('Needs MagicDNS and HTTPS switched on in the Tailscale admin console; a Headscale offers none, and the node then serves plain HTTP on 80. The tailnet encrypts every connection anyway; HTTPS adds a browser-trusted certificate and lets passkeys work on the tailnet name.')}</p>
        <label class="ts-check ts-indent"><input type="checkbox" bind:checked={form.redirect} disabled={!form.https} data-testid="st-redirect" /> {t('Redirect port 80 to HTTPS while a certificate is there')}</label>
        <label for="st-server">{t('Login server')}</label>
        <div class="ts-radios">
            <label class="ts-check"><input type="radio" name="st-server" value="tailscale" bind:group={server} disabled={locked} data-testid="st-server-tailscale" /> {t('Tailscale (login.tailscale.com)')}</label>
            <label class="ts-check"><input type="radio" name="st-server" value="headscale" bind:group={server} disabled={locked} data-testid="st-server-headscale" /> {t('Own Headscale server')}</label>
        </div>
        {#if server === 'headscale'}
            <label for="st-url">{t('Headscale URL')}</label>
            <input class="hmm-input" id="st-url" type="url" bind:value={form.control_url} placeholder="https://headscale.example.org" spellcheck="false" autocomplete="off" disabled={locked} data-testid="st-url" />
        {/if}
        <p class="ts-help">{locked ? t('The node is registered with this login server; log out (Connection → Log out and forget this device) to change it.') : t('A node belongs to one login server. Tailscale Inc. runs login.tailscale.com; a Headscale is your own control server with the same apps.')}</p>
        <label for="st-who">{t('Who may connect')}</label>
        <div class="ts-radios">
            <label class="ts-check"><input type="radio" name="st-who" value="all" bind:group={form.who} data-testid="st-who-all" /> {t('Every device the tailnet policy lets through')}</label>
            <label class="ts-check"><input type="radio" name="st-who" value="users" bind:group={form.who} data-testid="st-who-users" /> {t('Only devices of these users or with these tags')}</label>
        </div>
        {#if form.who === 'users'}
            <label for="st-users">{t('Users and tags')}</label>
            <textarea class="hmm-input ts-users" id="st-users" rows="4" bind:value={users} spellcheck="false" placeholder={'alice@example.com\ntag:phone'} data-testid="st-users"></textarea>
            <p class="ts-help">{t('One per line: a login name as the admin console shows it (alice@example.com; on a Headscale the user name), or a tag (tag:phone). Every connection is checked against the tailnet\'s own record of who the device belongs to; others are refused before they reach the web server.')}</p>
        {/if}
    </div>
    <div class="ts-actions">
        <button class="hmm-button ts-primary" disabled={!dirty || !valid || busy} onclick={save} data-testid="st-save">{t('Save')}</button>
    </div>
    <p class="ts-help">{t('Through the tailnet only this system is reachable: the user interface, the APIs and the addon pages. Not the home network behind it, not SSH, not the classic RPC. No exit node, no subnet routes, no Funnel.')}</p>
</section>

<section class="ts-section" data-testid="about">
    <details class="ts-details">
        <summary>{t('What the login server learns')}</summary>
        <p class="ts-help">{t('consent.learns')}</p>
        <p class="ts-help">{t('consent.not')}</p>
        <p class="ts-help">{t('The node\'s keys and identity are stored in /usr/local/etc/config/addons/tailscale/state and are in the system\'s backup: a restore brings the same node back. Whoever reads an unencrypted backup can appear on your tailnet as this system; switch backup encryption on.')}</p>
    </details>
</section>
