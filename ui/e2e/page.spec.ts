import {expect, test} from '@playwright/test';

// One run through the page against the daemon with the demo node (scripts/dev-serve.sh): the
// consent card, the connect, the login card with its link and QR code, the key login, the devices,
// the settings, the logout and the forget. The steps depend on each other (one daemon, one
// configuration).
test.describe.configure({mode: 'serial'});

const FAKE = 'http://127.0.0.1:18080';

test('starts with the consent card and contacts nothing', async ({page}) => {
    await page.goto('./');
    await expect(page.getByTestId('consent')).toBeVisible();
    await expect(page.getByTestId('connection')).toHaveCount(0);
    // a Headscale needs a URL before Connect is possible
    await page.getByTestId('server-headscale').check();
    await expect(page.getByTestId('consent-connect')).toBeDisabled();
    await page.getByTestId('consent-url').fill('https://headscale.example.org');
    await expect(page.getByTestId('consent-connect')).toBeEnabled();
    await page.getByTestId('server-tailscale').check();
    await page.getByTestId('consent-connect').click();
    await expect(page.getByTestId('connection')).toBeVisible();
    await expect(page.getByTestId('conn-state')).toHaveText('waiting for login');
});

test('shows the login link with its QR code, logs in with a key', async ({page}) => {
    await page.goto('./');
    await expect(page.getByTestId('login')).toBeVisible();
    await expect(page.getByTestId('login-open')).toHaveAttribute('href', /^https:\/\/login\.tailscale\.com\/a\//);
    await expect(page.getByTestId('login-qr')).toHaveAttribute('src', /^data:image\/png;base64,/);
    // the journal never holds the link; the page does, once
    await expect(page.getByTestId('login-url')).toContainText('login.tailscale.com');
    await page.getByText('Use an auth key instead').click();
    await page.getByTestId('login-key').fill('not-a-key-at-all');
    await page.getByTestId('login-with-key').click();
    await expect(page.getByText(/refused/)).toBeVisible();
    await page.getByTestId('login-key').fill('tskey-auth-k0123456789');
    await page.getByTestId('login-with-key').click();
    await expect(page.getByTestId('conn-state')).toHaveText('connected');
    await expect(page.getByTestId('conn-link')).toHaveAttribute('href', 'https://ccu.tail1234.ts.net/');
    await expect(page.getByTestId('conn-https')).toHaveText('https');
    await expect(page.getByTestId('conn-expiry')).toContainText('in 143 days');
    await expect(page.getByTestId('login')).toHaveCount(0);
});

test('lists the devices with their state and whether they may connect', async ({page}) => {
    await page.goto('./');
    await page.getByRole('tab', {name: /Devices/}).click();
    await expect(page.getByTestId('peer-pixel-7')).toBeVisible();
    await expect(page.getByTestId('peer-state-pixel-7')).toHaveText('online');
    await expect(page.getByTestId('peer-state-nas')).toHaveText('offline');
    await expect(page.getByTestId('peer-allowed-nas')).toHaveText('yes');
    await expect(page.getByText('via relay fra')).toBeVisible();
});

test('narrows who may connect and keeps the login server locked', async ({page}) => {
    await page.goto('./');
    await page.getByRole('tab', {name: /Settings/}).click();
    await expect(page.getByTestId('st-server-headscale')).toBeDisabled();
    await page.getByTestId('st-who-users').check();
    await expect(page.getByTestId('st-save')).toBeDisabled();
    await page.getByTestId('st-users').fill('sebastian@example.com\n');
    await page.getByTestId('st-save').click();
    await expect(page.getByTestId('st-save')).toBeDisabled();
    await page.getByRole('tab', {name: /Devices/}).click();
    await expect(page.getByTestId('peer-allowed-nas')).toHaveText('no');
    await expect(page.getByTestId('peer-allowed-pixel-7')).toHaveText('yes');
    // a bad hostname cannot be saved
    await page.getByRole('tab', {name: /Settings/}).click();
    await page.getByTestId('st-hostname').fill('CCU');
    await expect(page.getByTestId('st-save')).toBeDisabled();
    await page.getByTestId('st-hostname').fill('ccu-keller');
    await page.getByTestId('st-https').uncheck();
    await page.getByTestId('st-save').click();
    await expect(page.getByTestId('st-save')).toBeDisabled();
    await page.getByRole('tab', {name: /Connection/}).click();
    await expect(page.getByTestId('conn-link')).toHaveAttribute('href', 'http://ccu-keller.tail1234.ts.net/');
    await expect(page.getByTestId('conn-https')).toHaveText('http');
});

test('the backup notice', async ({page}) => {
    await page.request.get(`${FAKE}/dev/set?backup_unencrypted=1`);
    await page.goto('./');
    await expect(page.getByTestId('banner-backup')).toBeVisible({timeout: 25_000});
    await page.request.get(`${FAKE}/dev/set?backup_unencrypted=0`);
});

test('disconnects, reconnects, logs out and forgets', async ({page}) => {
    await page.goto('./');
    await page.getByTestId('conn-disconnect').click();
    await expect(page.getByTestId('conn-state')).toHaveText('off');
    await page.getByTestId('conn-connect').click();
    await expect(page.getByTestId('conn-state')).toHaveText('connected');
    await page.getByTestId('conn-logout').click();
    await expect(page.getByTestId('confirm-logout')).toBeVisible();
    await page.getByTestId('confirm-logout-yes').click();
    await expect(page.getByTestId('conn-state')).toHaveText('waiting for login');
    await page.getByTestId('login-request').click();
    await expect(page.getByTestId('login-open')).toBeVisible();
    await page.getByText('Use an auth key instead').click();
    await page.getByTestId('login-key').fill('tskey-auth-k0123456789');
    await page.getByTestId('login-with-key').click();
    await expect(page.getByTestId('conn-state')).toHaveText('connected');
    await page.getByTestId('conn-forget').click();
    await page.getByTestId('confirm-forget-yes').click();
    await expect(page.getByTestId('conn-state')).toHaveText('off');
    await expect(page.getByTestId('conn-forget')).toHaveCount(0);
    // the login server is free again
    await page.getByRole('tab', {name: /Settings/}).click();
    await expect(page.getByTestId('st-server-headscale')).toBeEnabled();
});
