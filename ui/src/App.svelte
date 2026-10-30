<script lang="ts">
    // The page: the connection (the consent card before the first connect, then the state, the
    // login, the actions), the devices on the tailnet, the settings. The banners on top say what
    // keeps the node from working.
    import {onMount} from 'svelte';
    import {Notices, Tabs, ThemeSwitch} from 'occulite-ui';
    import {i18n, t} from 'occulite-ui/i18n';
    import {NoticesStore} from 'occulite-ui/notices';
    import {cycleTheme, theme} from 'occulite-ui/theme';

    import ConnectionTab from './ConnectionTab.svelte';
    import DevicesTab from './DevicesTab.svelte';
    import SettingsTab from './SettingsTab.svelte';
    import {get} from './api.js';
    import {connState, daysUntil, type Config, type Facts, type Status} from './model.js';

    const notices = new NoticesStore();
    let tab = $state('connection');
    let config = $state<Config | undefined>();
    let status = $state<Status | undefined>();
    let facts = $state<Facts | undefined>();
    let loadError = $state('');
    let now = $state(Date.now());

    async function loadConfig() {
        try {
            config = await get<Config>('api/v1/config');
            loadError = '';
        } catch (e) {
            loadError = (e as Error).message;
        }
    }

    async function loadStatus() {
        try {
            status = await get<Status>('api/v1/status');
        } catch (e) {
            loadError = (e as Error).message;
        }
    }

    async function loadFacts(refresh = false) {
        try {
            facts = await get<Facts>('api/v1/system' + (refresh ? '?refresh=1' : ''));
        } catch {
            /* the banners simply stay away */
        }
    }

    onMount(() => {
        void loadConfig();
        void loadStatus();
        void loadFacts();
        const poll = setInterval(() => void loadStatus(), 3000);
        const slow = setInterval(() => void loadFacts(), 20000);
        const tick = setInterval(() => (now = Date.now()), 1000);
        return () => {
            clearInterval(poll);
            clearInterval(slow);
            clearInterval(tick);
        };
    });

    function changed(c: Config) {
        config = c;
        void loadStatus();
    }

    function statusChanged(s: Status) {
        status = s;
        void loadConfig();
    }

    const conn = $derived(connState(status));
    const expiryDays = $derived(status?.self?.key_expiry ? daysUntil(status.self.key_expiry, status.now) : undefined);
    const tabs = $derived([
        {id: 'connection', label: t('Connection'), ...(config && !config.consented ? {badge: '!'} : {})},
        {id: 'devices', label: t('Devices'), ...(status && status.peers.length ? {badge: status.peers.filter((p) => p.online).length} : {})},
        {id: 'settings', label: t('Settings')},
    ]);
</script>

<div class="ts-app" lang={i18n.language}>
    <header class="ts-header">
        <h1>{t('Remote access (Tailscale)')}</h1>
        <Tabs {tabs} bind:active={tab} label={t('Remote access (Tailscale)')} testId="tabs" />
        <span class="ts-spacer"></span>
        {#if config}<span class="ts-muted ts-small" title={t('Tailscale client {v}', {v: config.client_version})}>{t('Version {v}', {v: config.version})}</span>{/if}
        {#if !theme.followsShell}
            <ThemeSwitch theme={theme.choice} onclick={cycleTheme} />
        {/if}
    </header>
    <div class="ts-banners">
        {#if loadError}
            <div class="ts-banner ts-banner-error" role="alert">{t('The page could not read the configuration: {error}', {error: loadError})}</div>
        {/if}
        {#if status?.error && status.enabled && conn === 'error'}
            <div class="ts-banner ts-banner-error" role="alert" data-testid="banner-node">{t('The node is not running: {error}', {error: status.error})}</div>
        {/if}
        {#if status?.socket_error}
            <div class="ts-banner ts-banner-error" role="alert" data-testid="banner-socket">{t('The web server could not be reached through its ingress socket: {error}. The system needs openccu-lite 1.1.0-dev.6 or newer.', {error: status.socket_error})}</div>
        {/if}
        {#if conn === 'expired'}
            <div class="ts-banner ts-banner-error" role="alert" data-testid="banner-expired">{t("This node's key has expired: it is not reachable on the tailnet until you log in again (Connection → Log in again), or disable key expiry for this device in the admin console.")}</div>
        {:else if conn === 'connected' && expiryDays !== undefined && expiryDays <= 14}
            <div class="ts-banner ts-banner-warn" role="status" data-testid="banner-expiry">{t("This node's key expires in {n} days. Log in again before then (Connection → Log in again), or disable key expiry for this device in the admin console.", {n: Math.max(0, expiryDays)})}</div>
        {/if}
        {#if conn === 'approval'}
            <div class="ts-banner ts-banner-info" role="status" data-testid="banner-approval">{t('The node is registered and waits for approval: your tailnet requires an administrator to approve new devices. Approve it in the admin console (Machines) or with headscale nodes register.')}</div>
        {/if}
        {#if facts?.backup_unencrypted && config?.consented}
            <div class="ts-banner ts-banner-warn" role="status" data-testid="banner-backup">
                {t("The backups of this system are not encrypted. The node's keys are in every backup; whoever reads a backup can appear on your tailnet as this system. Switch backup encryption on.")}
                <a href="/backup#encryption" target="_top">{t('Backup page')}</a>
            </div>
        {/if}
        {#if facts?.checked && !facts.token_ok}
            <div class="ts-banner ts-banner-warn" role="status" data-testid="banner-token">{t("The addon's token is not accepted by the system: {error}. The page cannot show the backup state.", {error: facts.error ?? ''})}</div>
        {/if}
    </div>
    <main class="ts-page">
        {#if config}
            {#if tab === 'devices'}
                <DevicesTab {config} {status} {now} onselect={(id) => (tab = id)} />
            {:else if tab === 'settings'}
                <SettingsTab {config} {status} {notices} onchanged={changed} onstatus={statusChanged} />
            {:else}
                <ConnectionTab {config} {status} {notices} {now} onchanged={changed} onstatus={statusChanged} />
            {/if}
        {:else if !loadError}
            <p class="ts-muted">{t('Loading…')}</p>
        {/if}
    </main>
    <Notices notices={notices.visible} ondismiss={(id) => notices.dismiss(id)} />
</div>
