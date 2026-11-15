import assert from 'node:assert/strict';
import { chromium } from 'playwright';

const base = process.env.BASE_URL || 'https://localhost:3000';
const host = new URL(base).hostname;
const executablePath = process.env.CHROME_PATH || '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome';
const archiveId = '11111111-1111-4111-8111-111111111111';
const address = 'm4x7q2v6n3jk5wzp@inbound.invoyis.com';
const rotated = 'aaaaaaaaaaaaaaaa@inbound.invoyis.com';
const browser = await chromium.launch({ headless: true, executablePath });
try {
  for (const locale of ['en', 'fr', 'he']) {
    const page = await browser.newPage({ ignoreHTTPSErrors: true });
    await page.context().addCookies([{ name: 'invoyis_locale', value: locale, domain: host, path: '/' }]);
    await page.addInitScript(() => {
      Object.defineProperty(navigator, 'clipboard', { value: { writeText: async text => { window.__inboundCopied = text; } } });
    });
    const archive = { id: archiveId, name: 'Synthetic archive', kind: 'personal', role: 'admin', jurisdiction: null, canBulkExport: false };
    let current = { enabled: true, address };
    await page.route('**/api/v1/archives**', async route => {
      const path = new URL(route.request().url()).pathname;
      if (path === '/api/v1/archives') return route.fulfill({ json: { items: [archive] } });
      if (path === `/api/v1/archives/${archiveId}`) return route.fulfill({ json: archive });
      if (path.endsWith('/document-counts')) return route.fulfill({ json: { total: 0, needsReview: 0, attention: 0 } });
      if (path.endsWith('/document-folders')) return route.fulfill({ json: { by: 'company', items: [], unassigned: 0, total: 0 } });
      if (path.endsWith('/forwarding')) return route.fulfill({ json: current });
      if (path.endsWith('/forwarding/disable')) current = { ...current, enabled: false };
      if (path.endsWith('/forwarding/enable')) current = { ...current, enabled: true };
      if (path.endsWith('/forwarding/rotate')) current = { enabled: true, address: rotated };
      if (path.includes('/forwarding/')) return route.fulfill({ json: current });
      if (path.endsWith('/inbound-deliveries')) return route.fulfill({ json: { items: [{ id: 'a'.repeat(64), outcome: 'upload_guidance', acceptedCount: 0, rejectedCount: 0, receivedAt: '2026-11-15T12:00:00Z' }] } });
      if (path.endsWith('/documents')) return route.fulfill({ json: { items: [] } });
      return route.fulfill({ status: 404, json: { code: 'NOT_FOUND' } });
    });
    await page.goto(`${base}/archives/${archiveId}`);
    page.setDefaultTimeout(8000);
    await page.getByText('Synthetic archive').first().waitFor();
    assert.equal(await page.locator('html').getAttribute('dir'), locale === 'he' ? 'rtl' : 'ltr');
    await page.getByRole('button', { name: locale === 'en' ? 'Archive settings' : locale === 'fr' ? 'Paramètres de l’archive' : 'הגדרות ארכיון' }).click();
    const dialog = page.getByRole('dialog');
    await dialog.locator('bdi[dir="ltr"]').getByText(address).waitFor();
    assert.equal(await dialog.locator('bdi[dir="ltr"]').evaluate(node => getComputedStyle(node).unicodeBidi), 'isolate');
    await dialog.getByRole('button', { name: locale === 'en' ? 'Copy address' : locale === 'fr' ? 'Copier l’adresse' : 'העתקת כתובת' }).click();
    assert.equal(await page.evaluate(() => window.__inboundCopied), address);
    await dialog.getByRole('button', { name: locale === 'en' ? 'Rotate address' : locale === 'fr' ? 'Changer l’adresse' : 'החלפת כתובת' }).click();
    await dialog.locator('bdi[dir="ltr"]').getByText(rotated).waitFor();
    await page.goto(`${base}/archives/${archiveId}/inbound`);
    await page.getByText(locale === 'en' ? /Upload the document directly/ : locale === 'fr' ? /Importez le document directement/ : /להעלות את המסמך ישירות/).waitFor();
    await page.close();
    console.log(`${locale}: admin controls, exact clipboard, LTR isolation, outcomes and page direction passed`);
  }
} finally { await browser.close(); }
