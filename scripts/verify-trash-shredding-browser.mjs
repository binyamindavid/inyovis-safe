import assert from 'node:assert/strict';
import { mkdir } from 'node:fs/promises';
import { chromium } from 'playwright';

const base = process.env.BASE_URL ?? 'https://localhost:3000';
const output = process.env.SCREENSHOTS ?? 'docs/screenshots/trash-shredding';
await mkdir(output, { recursive: true });

const archiveId = '11111111-1111-4111-8111-111111111111';
const revision = '22222222-2222-4222-8222-222222222222';
const archive = { id: archiveId, name: 'Personal archive', kind: 'personal', role: 'admin' };
const trashedAt = new Date().toISOString();
const recoveryUntil = new Date(Date.now() + 29 * 86400_000).toISOString();
const fixtures = ['lease.pdf', 'facture.pdf', 'מסמך.pdf'].map((name, index) => ({
  id: `33333333-3333-4333-8333-33333333333${index}`, name, trashedAt, recoveryUntil,
}));
const words = {
  en: { shred: 'Shred permanently', empty: 'Empty Trash', count: '2 eligible · 1 blocked', confirm: 'Confirm permanent shredding', cancel: 'Cancel', close: 'Close', results: 'Shredding results', pending: 'Pending', failed: 'Failed', verified: 'Verified', hold: 'An active hold blocks shredding.', changed: 'The document or its Trash entry changed.', restore: 'Restore' },
  fr: { shred: 'Détruire définitivement', empty: 'Vider la corbeille', count: '2 admissible(s) · 1 bloqué(s)', confirm: 'Confirmer la destruction définitive', cancel: 'Annuler', close: 'Fermer', results: 'Résultats de la destruction', pending: 'En attente', failed: 'Échec', verified: 'Vérifié', hold: 'Une suspension active bloque la destruction.', changed: 'Le document ou sa mise à la corbeille a changé.', restore: 'Restaurer' },
  he: { shred: 'גריסה לצמיתות', empty: 'ריקון האשפה', count: '2 כשירים לגריסה · 1 חסומים', confirm: 'אישור גריסה לצמיתות', cancel: 'ביטול', close: 'סגירה', results: 'תוצאות הגריסה', pending: 'ממתין', failed: 'נכשל', verified: 'אומת', hold: 'עיכוב פעיל מונע גריסה.', changed: 'המסמך או הרשומה שלו באשפה השתנו.', restore: 'שחזור' },
};
const browser = await chromium.launch({ headless: true, channel: 'chrome' });
try {
  for (const [locale, t] of Object.entries(words)) {
    for (const [device, width, height] of [['desktop', 1440, 900], ['mobile', 390, 844]]) {
      const context = await browser.newContext({ viewport: { width, height }, ignoreHTTPSErrors: true });
      const page = await context.newPage();
      await context.addCookies([{ name: 'invoyis_locale', value: locale, url: base }]);
      let submission;
      let singleSubmission;
      let status = 'pending';
      await page.route('**/api/v1/archives**', route => {
        const { pathname, searchParams } = new URL(route.request().url());
        if (pathname.endsWith('/trash/shred')) {
          if (route.request().method() === 'POST') {
            const request = route.request().postDataJSON();
            if (request.confirmedCount === 1) {
              singleSubmission = request;
              return route.fulfill({ json: { items: [{ id: fixtures[1].id, status: 'pending' }] } });
            }
            submission = request;
            return route.fulfill({ json: { items: [
              { id: fixtures[0].id, status: 'pending' },
              { id: fixtures[1].id, status: 'blocked', blocker: 'document_changed' },
            ] } });
          }
          const items = fixtures.filter(item => !searchParams.has('documentId') || item.id === searchParams.get('documentId'))
            .map(({ recoveryUntil: _, ...item }) => item.id === fixtures[2].id
              ? { ...item, revision, status: 'blocked', blocker: 'active_hold' }
              : { ...item, revision, status: 'eligible' });
          return route.fulfill({ json: { items, eligible: items.filter(item => item.status === 'eligible').length, blocked: items.filter(item => item.status === 'blocked').length } });
        }
        if (pathname.endsWith('/trash')) {
          return route.fulfill({ json: {
            canShred: true, items: singleSubmission ? fixtures.slice(2) : submission ? fixtures.slice(1) : fixtures,
            shredding: [
              ...(submission ? [{ id: fixtures[0].id, status, requestedAt: trashedAt }] : []),
              ...(singleSubmission ? [{ id: fixtures[1].id, status: 'pending', requestedAt: trashedAt }] : []),
            ],
          } });
        }
        if (pathname.endsWith('/document-counts')) return route.fulfill({ json: { total: 0, needsReview: 0 } });
        if (pathname.endsWith(archiveId)) return route.fulfill({ json: archive });
        if (pathname.endsWith('/archives')) return route.fulfill({ json: { items: [archive] } });
        return route.fallback();
      });
      await page.goto(`${base}/archives/${archiveId}/trash`);
      const row = page.getByRole('listitem').filter({ hasText: fixtures[0].name });
      const shred = row.getByRole('button', { name: t.shred });
      await shred.waitFor();
      assert.equal(await page.locator('main').getAttribute('dir'), locale === 'he' ? 'rtl' : 'ltr');
      assert.equal(await row.getByRole('button', { name: t.restore }).isEnabled(), true);
      await shred.focus();
      await page.keyboard.press('Enter');
      const dialog = page.getByRole('dialog');
      await dialog.getByRole('button', { name: t.confirm }).waitFor();
      assert.equal(await dialog.getByRole('button', { name: t.cancel }).evaluate(node => node === document.activeElement), true);
      await page.keyboard.press('Escape');
      await dialog.waitFor({ state: 'hidden' });
      await page.waitForFunction(() => document.activeElement?.textContent === document.querySelector('main li button:last-child')?.textContent);
      assert.equal(submission, undefined);

      const empty = page.getByRole('button', { name: t.empty });
      await empty.focus();
      await page.keyboard.press('Space');
      await dialog.getByText(t.count).waitFor();
      await dialog.getByText(t.hold, { exact: false }).waitFor();
      assert.equal(await dialog.locator('[dir]').first().getAttribute('dir'), locale === 'he' ? 'rtl' : 'ltr');
      await page.screenshot({ path: `${output}/${locale}-${device}-confirmation.png` });
      await page.keyboard.press('Tab');
      const confirm = dialog.getByRole('button', { name: t.confirm });
      assert.equal(await confirm.evaluate(node => node === document.activeElement), true);
      await page.keyboard.press('Enter');
      await dialog.getByRole('heading', { name: t.results }).waitFor();
      await dialog.getByText(t.changed, { exact: false }).waitFor();
      assert.deepEqual(submission, {
        confirmedCount: 2,
        targets: fixtures.slice(0, 2).map(({ recoveryUntil: _, ...item }) => ({ ...item, revision })),
      });
      await dialog.getByRole('button', { name: t.close }).focus();
      await page.keyboard.press('Enter');
      await dialog.waitFor({ state: 'hidden' });
      const history = page.locator('section[aria-labelledby="trash-shred-history"]');
      await history.getByText(t.pending, { exact: false }).waitFor();
      assert.equal(await page.getByRole('button', { name: t.restore }).count(), 2);
      assert.equal(await page.locator('main').evaluate(node => node.scrollWidth <= node.clientWidth), true);
      await page.screenshot({ path: `${output}/${locale}-${device}-pending.png` });
      if (locale === 'en' && device === 'desktop') {
        status = 'failed';
        await history.getByText(t.failed, { exact: false }).waitFor();
        await page.screenshot({ path: `${output}/en-desktop-failed.png` });
        status = 'verified';
        await history.getByText(t.verified, { exact: false }).waitFor();
        await page.screenshot({ path: `${output}/en-desktop-verified.png` });
      }
      const remaining = page.getByRole('listitem').filter({ hasText: fixtures[1].name });
      await remaining.getByRole('button', { name: t.shred }).focus();
      await page.keyboard.press('Enter');
      await confirm.waitFor();
      await page.keyboard.press('Tab');
      assert.equal(await confirm.evaluate(node => node === document.activeElement), true);
      await page.keyboard.press('Enter');
      await dialog.getByRole('heading', { name: t.results }).waitFor();
      const { recoveryUntil: _, ...target } = fixtures[1];
      assert.deepEqual(singleSubmission, { confirmedCount: 1, targets: [{ ...target, revision }] });
      await context.close();
      console.log(`${locale} ${device} passed. Keyboard cancel, explicit confirmation, mixed results, and Restore availability.`);
    }
  }
} finally {
  await browser.close();
}
