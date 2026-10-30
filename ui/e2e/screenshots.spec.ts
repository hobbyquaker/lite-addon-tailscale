import {expect, test} from '@playwright/test';

// The readme's screenshots (SCREENSHOTS=1 npx playwright test screenshots): German, light and dark,
// against the demo node.
test.skip(!process.env.SCREENSHOTS, 'only on demand');
test.use({locale: 'de-DE', viewport: {width: 1200, height: 820}});

const FAKE = 'http://127.0.0.1:18080';

for (const scheme of ['light', 'dark'] as const) {
    test(`screenshots ${scheme}`, async ({page}) => {
        await page.emulateMedia({colorScheme: scheme});
        const suffix = scheme === 'dark' ? '-dunkel' : '';
        const out = (n: string) => `../docs/screenshots/${n}${suffix}.png`;
        await page.request.get(`${FAKE}/dev/set?backup_unencrypted=0`);
        await page.goto('./');
        if (await page.getByTestId('consent').count()) {
            await page.waitForTimeout(400);
            await page.screenshot({path: out('01-einwilligung')});
            await page.getByTestId('consent-connect').click();
        } else {
            // a second run: log out to get the login card again
            if (await page.getByTestId('conn-logout').count()) {
                await page.getByTestId('conn-logout').click();
                await page.getByTestId('confirm-logout-yes').click();
            }
            if (await page.getByTestId('conn-connect').count()) {
                await page.getByTestId('conn-connect').click();
            }
            if (!(await page.getByTestId('login-open').count())) {
                await page.getByTestId('login-request').click();
            }
        }
        await expect(page.getByTestId('login-open')).toBeVisible();
        // a reload clears the notices
        await page.reload();
        await expect(page.getByTestId('login-open')).toBeVisible();
        await page.waitForTimeout(400);
        await page.screenshot({path: out('02-anmeldung')});
        await page.getByText('Stattdessen einen Auth-Key verwenden').click();
        await page.getByTestId('login-key').fill('tskey-auth-k0123456789');
        await page.getByTestId('login-with-key').click();
        await expect(page.getByTestId('conn-state')).toHaveText('verbunden');
        await page.reload();
        await expect(page.getByTestId('conn-state')).toHaveText('verbunden');
        await page.waitForTimeout(600);
        await page.screenshot({path: out('03-verbindung')});
        await page.getByRole('tab', {name: /Geräte/}).click();
        await page.waitForTimeout(400);
        await page.screenshot({path: out('04-geraete')});
        await page.getByRole('tab', {name: /Einstellungen/}).click();
        await page.waitForTimeout(400);
        await page.screenshot({path: out('05-einstellungen')});
        // leave the node logged out for the second scheme
        await page.getByRole('tab', {name: /Verbindung/}).click();
        await page.getByTestId('conn-logout').click();
        await page.getByTestId('confirm-logout-yes').click();
        await expect(page.getByTestId('conn-state')).toHaveText('wartet auf Anmeldung');
    });
}
