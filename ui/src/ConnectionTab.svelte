<script lang="ts">
    // The connection: the consent card before the first connect; then the state, the login (URL
    // with QR code, or an auth key), what the system is called on the tailnet, the key expiry, and
    // the actions (connect, disconnect, log in again, log out).
    import {copyText} from 'occulite-ui/clipboard';
    import {i18n, t} from 'occulite-ui/i18n';
    import type {NoticesStore} from 'occulite-ui/notices';

    import ConfirmDialog from './ConfirmDialog.svelte';
    import {post} from './api.js';
    import {connState, formatAge, formatDate, formatExpiry, looksLikeURL, tailnetLink, type Config, type Status} from './model.js';

    let {config, status, notices, now, onchanged, onstatus}: {config: Config; status: Status | undefined; notices: NoticesStore; now: number; onchanged: (c: Config) => void; onstatus: (s: Status) => void} = $props();

    let busy = $state(false);
    let host = $state<HTMLElement | undefined>();
    // the consent card
    let server = $state<'tailscale' | 'headscale'>('tailscale');
    let headscale = $state('');
    let consentKey = $state('');
    // the login card
    let authKey = $state('');
    let confirmLogout = $state(false);
    let confirmForget = $state(false);

    const conn = $derived(connState(status));
    // the daemon's clock, moved on by the page's own ticks since the answer
    let loaded = $state(Date.now());
    $effect(() => {
        if (status) loaded = Date.now();
    });
    const serverNow = $derived(status ? status.now + (now - loaded) : now);
    const link = $derived(tailnetLink(status));
    const consentValid = $derived(server === 'tailscale' || looksLikeURL(headscale));
    const loginServer = $derived(status?.control_url || config.control_url || '');

    const hints: Record<string, string> = {
        off: 'The node is switched off: nothing is contacted, this system is not on the tailnet.',
        starting: 'The node starts and contacts the login server.',
        login: 'The node needs a login: open the link or scan the code with a device that is logged in to your tailnet, or use an auth key.',
        approval: 'The node is registered and waits for an administrator to approve it in the admin console.',
        connected: 'The node is on the tailnet and reachable under its name.',
        offline: 'The node is logged in but the login server does not see it right now; the network connection may be down.',
        expired: "The node's key has expired: log in again, or disable key expiry for this device in the admin console.",
        error: 'The node could not start; the error is in the banner above and in the journal.',
    };

    async function act(path: string, body: unknown, done?: string) {
        busy = true;
        try {
            onstatus(await post<Status>(path, body));
            if (done) notices.push('info', done);
            return true;
        } catch (e) {
            notices.push('error', (e as Error).message);
            return false;
        } finally {
            busy = false;
        }
    }

    async function consent() {
        const body = {control_url: server === 'headscale' ? headscale.trim() : '', auth_key: consentKey.trim()};
        if (await act('api/v1/connect', body)) {
            consentKey = '';
            notices.push('info', t('The node starts and contacts the login server.'));
        }
    }

    async function loginWithKey() {
        if (await act('api/v1/login', {auth_key: authKey.trim()}, t('The key was sent to the login server.'))) authKey = '';
    }

    async function copy(text: string) {
        const r = await copyText(text, {host});
        notices.push(r === 'failed' ? 'warn' : 'info', r === 'failed' ? t('The clipboard is not available here; select the text and copy it.') : t('Copied.'));
    }

    async function logout(forget: boolean) {
        confirmLogout = false;
        confirmForget = false;
        if (await act('api/v1/logout', {forget}, forget ? t('The node is logged out, its identity removed and the addon switched off.') : t('The node is logged out; it needs a new login.'))) {
            /* the status carries the new state */
        }
    }
</script>

{#if !config.consented}
    <section class="ts-section ts-consent" data-testid="consent" bind:this={host}>
        <h2>{t('Before the first connection')}</h2>
        <p>{t('consent.1')}</p>
        <ul>
            <li>{t('consent.learns')}</li>
            <li>{t('consent.not')}</li>
            <li>{t('consent.reach')}</li>
            <li>{t('consent.ct')}</li>
        </ul>
        <div class="ts-radios">
            <label class="ts-check"><input type="radio" name="server" value="tailscale" bind:group={server} data-testid="server-tailscale" /> {t('Tailscale (login.tailscale.com; an account with Tailscale Inc.)')}</label>
            <label class="ts-check"><input type="radio" name="server" value="headscale" bind:group={server} data-testid="server-headscale" /> {t('Your own Headscale server (nothing is contacted but it and its relays)')}</label>
        </div>
        {#if server === 'headscale'}
            <div class="ts-form">
                <label for="hs-url">{t('Login server')}</label>
                <input class="hmm-input" id="hs-url" type="url" bind:value={headscale} placeholder="https://headscale.example.org" spellcheck="false" autocomplete="off" data-testid="consent-url" />
            </div>
        {/if}
        <details class="ts-details">
            <summary>{t('I have an auth key')}</summary>
            <div class="ts-form">
                <label for="consent-key">{t('Auth key')}</label>
                <input class="hmm-input ts-mono" id="consent-key" type="password" bind:value={consentKey} placeholder="tskey-auth-…" spellcheck="false" autocomplete="off" data-testid="consent-key" />
                <p class="ts-help">{t('A pre-authorised key from the admin console (Settings → Keys) or from headscale preauthkeys create. Used once for the registration and never stored. A tagged key (tag:ccu, say) gives a node without key expiry.')}</p>
            </div>
        </details>
        <div class="ts-actions">
            <button class="hmm-button ts-primary" onclick={consent} disabled={busy || !consentValid} data-testid="consent-connect">{t('Connect')}</button>
        </div>
    </section>
{:else}
    <section class="ts-section" data-testid="connection" bind:this={host}>
        <h2>{t('Connection')}</h2>
        <div class="ts-kv">
            <span>{t('State')}</span>
            <span class="ts-row">
                <span class="ts-pill ts-pill-wide" class:ok={conn === 'connected'} class:warn={conn === 'login' || conn === 'approval' || conn === 'starting' || conn === 'offline'} class:bad={conn === 'error' || conn === 'expired'} class:off={conn === 'off'} title={t(hints[conn]!)} data-testid="conn-state">{t('conn.' + conn)}</span>
                {#if status?.since && status.running}<span class="ts-muted ts-small">{t('since {age}', {age: formatAge(status.since, serverNow, t)})}</span>{/if}
            </span>
            <span>{t('This system on the tailnet')}</span>
            <span class="ts-row">
                {#if link}
                    <a class="ts-mono" href={link} target="_blank" rel="noopener" data-testid="conn-link">{link}</a>
                    <button class="hmm-button" onclick={() => copy(link)}>{t('Copy')}</button>
                {:else}
                    <span class="ts-muted">–</span>
                {/if}
            </span>
            <span>{t('Tailnet')}</span>
            <span>{status?.self?.tailnet || status?.self?.magic_dns || '–'}{#if status?.self?.user}<span class="ts-muted"> · {t('user {u}', {u: status.self.user})}</span>{/if}{#if status?.self?.tags?.length}<span class="ts-muted"> · {status.self.tags.join(' ')}</span>{/if}</span>
            <span>{t('Tailnet addresses')}</span>
            <span class="ts-mono" data-testid="conn-addresses">{status?.self?.addresses.join(', ') || '–'}</span>
            <span>{t('Login server')}</span>
            <span class="ts-mono">{loginServer || 'login.tailscale.com'}</span>
            <span>{t('Key expiry')}</span>
            <span data-testid="conn-expiry" class:ts-expiry-warn={!!status?.self?.key_expiry && status.self.key_expiry - serverNow < 14 * 86_400_000}>
                {#if status?.self?.key_expiry}{formatDate(status.self.key_expiry, i18n.language)} · {formatExpiry(status.self.key_expiry, serverNow, t)}{:else}{t('never')}{/if}
            </span>
            <span>{t('HTTPS on the tailnet')}</span>
            <span class="ts-row">
                <span class="ts-pill" class:ok={status?.https} class:off={!status?.https} data-testid="conn-https">{status?.https ? 'https' : 'http'}</span>
                {#if status?.https && status.self?.cert_domain}<span class="ts-muted ts-small">{t('certificate for {name}; port 80 {redirect}', {name: status.self.cert_domain, redirect: config.redirect ? t('redirects') : t('stays plain')})}</span>
                {:else if config.https && status?.https_error === 'no-certificate'}<span class="ts-muted ts-small">{t('plain HTTP: the tailnet offers no certificate for this node (Tailscale: switch MagicDNS and HTTPS on in the admin console; Headscale offers none). The tailnet encrypts the connection anyway.')}</span>
                {:else if config.https && status?.https_error}<span class="ts-muted ts-small">{status.https_error}</span>
                {:else if !config.https}<span class="ts-muted ts-small">{t('switched off under Settings; the tailnet encrypts the connection anyway')}</span>{/if}
            </span>
            <span>{t('Connections')}</span>
            <span>{t('{n} open, {total} since the start, {refused} refused', {n: status?.connections ?? 0, total: status?.forwarded ?? 0, refused: status?.refused ?? 0})}</span>
            <span>{t('Tailscale client')}</span>
            <span>{config.client_version}{#if status?.self?.relay}<span class="ts-muted"> · {t('relay region {r}', {r: status.self.relay})}</span>{/if}</span>
        </div>
        {#if status?.health?.length && conn !== 'off'}
            <p class="ts-help" data-testid="conn-health">{status.health.join(' · ')}</p>
        {/if}
        <div class="ts-actions">
            {#if status?.enabled}
                <button class="hmm-button ts-fixed" onclick={() => act('api/v1/disconnect', undefined, t('The node is stopped.'))} disabled={busy} data-testid="conn-disconnect">{t('Disconnect')}</button>
            {:else}
                <button class="hmm-button ts-primary ts-fixed" onclick={() => act('api/v1/connect', {}, t('The node starts.'))} disabled={busy} data-testid="conn-connect">{t('Connect')}</button>
            {/if}
            {#if status?.running && status.has_identity && conn !== 'login'}
                <button class="hmm-button" onclick={() => act('api/v1/login', {}, t('A new login was requested.'))} disabled={busy} data-testid="conn-relogin">{t('Log in again…')}</button>
            {/if}
            <span class="ts-spacer"></span>
            {#if status?.has_identity}
                <button class="hmm-button" onclick={() => (confirmLogout = true)} disabled={busy} data-testid="conn-logout">{t('Log out…')}</button>
                <button class="hmm-button ts-danger" onclick={() => (confirmForget = true)} disabled={busy} data-testid="conn-forget">{t('Log out and forget this device…')}</button>
            {/if}
        </div>
    </section>

    {#if conn === 'login'}
        <section class="ts-section" data-testid="login">
            <h2>{t('Login')}</h2>
            {#if status?.login_url}
                <div class="ts-login">
                    {#if status.login_qr}<img class="ts-qr" src={status.login_qr} alt={t('QR code of the login link')} data-testid="login-qr" />{/if}
                    <div class="ts-login-body">
                        <p class="ts-help">{t('Open the link on a device that is logged in to your tailnet, or scan the code with the Tailscale app on your phone, and approve this system there. The link is valid for a few minutes.')}</p>
                        <div class="ts-row">
                            <a class="hmm-button ts-primary" href={status.login_url} target="_blank" rel="noopener" data-testid="login-open">{t('Open the login page')}</a>
                            <button class="hmm-button" onclick={() => copy(status!.login_url!)}>{t('Copy link')}</button>
                            <button class="hmm-button" onclick={() => act('api/v1/login', {}, t('A new login link was requested.'))} disabled={busy} data-testid="login-renew">{t('New link')}</button>
                        </div>
                        <div class="ts-url" data-testid="login-url">{status.login_url}</div>
                    </div>
                </div>
            {:else}
                <p class="ts-help">{t('No login link is open. Request one, or use an auth key.')}</p>
                <div class="ts-actions">
                    <button class="hmm-button ts-primary" onclick={() => act('api/v1/login', {}, t('A new login link was requested.'))} disabled={busy} data-testid="login-request">{t('Request a login link')}</button>
                </div>
            {/if}
            <details class="ts-details">
                <summary>{t('Use an auth key instead')}</summary>
                <div class="ts-form">
                    <label for="auth-key">{t('Auth key')}</label>
                    <input class="hmm-input ts-mono" id="auth-key" type="password" bind:value={authKey} placeholder="tskey-auth-…" spellcheck="false" autocomplete="off" data-testid="login-key" />
                    <p class="ts-help">{t('A pre-authorised key from the admin console (Settings → Keys) or from headscale preauthkeys create. Used once for the registration and never stored. A tagged key (tag:ccu, say) gives a node without key expiry.')}</p>
                </div>
                <div class="ts-actions">
                    <button class="hmm-button" onclick={loginWithKey} disabled={busy || authKey.trim().length < 8} data-testid="login-with-key">{t('Log in with the key')}</button>
                </div>
            </details>
        </section>
    {/if}

    <section class="ts-section" data-testid="howto">
        <details class="ts-details" open={conn === 'login' || conn === 'off'}>
            <summary>{t('How to reach this system')}</summary>
            <ol class="ts-howto">
                <li>{t('howto.1')}</li>
                <li>{t('howto.2')}</li>
                <li>{t('howto.3')}</li>
                <li>{t('howto.4')}</li>
            </ol>
            <p class="ts-help">{t('howto.5')}</p>
        </details>
    </section>
{/if}

<ConfirmDialog
    open={confirmLogout}
    title={t('Log out of the tailnet?')}
    message={t("The login server expires this node's key; this system leaves the tailnet until you log in again. The device stays listed in the admin console until you remove it there.")}
    confirmLabel={t('Log out')}
    testId="confirm-logout"
    onconfirm={() => logout(false)}
    onclose={() => (confirmLogout = false)}
/>
<ConfirmDialog
    open={confirmForget}
    title={t('Log out and forget this device?')}
    message={t("The node logs out, its keys and identity on this system are deleted and the addon is switched off. A later connect registers a new device (a new name and address on the tailnet). Remove the old device in the admin console.")}
    confirmLabel={t('Log out and forget')}
    danger
    testId="confirm-forget"
    onconfirm={() => logout(true)}
    onclose={() => (confirmForget = false)}
/>
