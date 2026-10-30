<script lang="ts">
    // The devices on the tailnet, read-only: what the login server lets this node see, with the
    // connection path, the traffic and whether the who-may-connect rule admits each.
    import {t} from 'occulite-ui/i18n';

    import {formatAge, formatBytes, pathOf, type Config, type Peer, type Status} from './model.js';

    let {config, status, now, onselect}: {config: Config; status: Status | undefined; now: number; onselect: (tab: string) => void} = $props();

    let loaded = $state(Date.now());
    $effect(() => {
        if (status) loaded = Date.now();
    });
    const serverNow = $derived(status ? status.now + (now - loaded) : now);

    function peerState(p: Peer): 'online' | 'offline' | 'expired' {
        if (p.expired) return 'expired';
        return p.online ? 'online' : 'offline';
    }

    function who(p: Peer): string {
        const parts: string[] = [];
        if (p.user) parts.push(p.user);
        if (p.tags?.length) parts.push(p.tags.join(' '));
        return parts.join(' · ') || '–';
    }
</script>

<section class="ts-section ts-section-wide" data-testid="devices">
    <h2>{t('Devices on the tailnet')}</h2>
    <p class="ts-help">
        {#if config.who === 'users'}
            {t('Only devices of these users or with these tags may connect: {list}. Every other device on the tailnet is refused by the addon, whatever the tailnet policy says.', {list: config.allowed_users.join(', ')})}
        {:else}
            {t('Every device the tailnet policy lets through may connect to this system. Narrow it to certain users or tags under Settings.')}
        {/if}
        <button class="hmm-button" onclick={() => onselect('settings')} data-testid="devices-settings">{t('Settings')}</button>
    </p>
    {#if !status?.running || !status.peers.length}
        <p class="ts-muted" data-testid="no-peers">{status?.running ? t('No other device on the tailnet yet. Install the Tailscale app on your phone or laptop and log in to the same tailnet.') : t('The node is not running.')}</p>
    {:else}
        <table class="ts-grid ts-peers">
            <thead><tr><th>{t('Name')}</th><th>{t('State')}</th><th>{t('User / tags')}</th><th>{t('Addresses')}</th><th>{t('Path')}</th><th>{t('Last seen')}</th><th>{t('Traffic')}</th><th>{t('May connect')}</th></tr></thead>
            <tbody>
                {#each status.peers as p (p.dns_name || p.name)}
                    {@const st = peerState(p)}
                    {@const path = pathOf(p.path)}
                    <tr class:ts-row-disabled={st !== 'online'} data-testid="peer-{p.name}">
                        <td>
                            <div class="ts-name" title={p.dns_name}>{p.name}</div>
                            <div class="ts-faint ts-nowrap">{p.os || '–'}</div>
                        </td>
                        <td><span class="ts-pill" class:ok={st === 'online'} class:bad={st === 'expired'} class:off={st === 'offline'} data-testid="peer-state-{p.name}">{t('peer.' + st)}</span></td>
                        <td class="ts-tags">{who(p)}</td>
                        <td class="ts-mono ts-nowrap">{p.addresses.join(' ')}</td>
                        <td class="ts-nowrap" title={path.detail}>{path.kind === 'direct' ? t('direct') : path.kind === 'relay' ? t('via relay {r}', {r: path.detail}) : '–'}</td>
                        <td class="ts-nowrap ts-age">{p.online ? t('now') : formatAge(p.last_seen, serverNow, t)}</td>
                        <td class="ts-nowrap ts-traffic" title="{t('received')} / {t('sent')}">
                            <span class="ts-arrow">↓</span>{formatBytes(p.rx_bytes)} <span class="ts-arrow">↑</span>{formatBytes(p.tx_bytes)}
                            {#if p.connections}<span class="ts-faint"> · {t('{n} connections', {n: p.connections})}</span>{/if}
                        </td>
                        <td><span class="ts-pill" class:ok={p.allowed} class:bad={!p.allowed} data-testid="peer-allowed-{p.name}">{p.allowed ? t('yes') : t('no')}</span></td>
                    </tr>
                {/each}
            </tbody>
        </table>
    {/if}
</section>
