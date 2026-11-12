import assert from 'node:assert/strict';
import { readFileSync, existsSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
import { chromium } from 'playwright';

const origin = process.env.ISSUE12_ORIGIN;
const database = process.env.ISSUE12_DATABASE_URL;
const mailpit = process.env.ISSUE12_MAILPIT_URL;
const firstFile = process.env.ISSUE12_PDF_ONE;
const secondFile = process.env.ISSUE12_PDF_TWO;
assert.match(origin ?? '', /^https:\/\/localhost:\d+$/);
assert.match(database ?? '', /invoyis_issue12_browser_test/);
assert.ok(mailpit && firstFile && secondFile);
const email = 'issue12-browser@example.test';
const query = sql => execFileSync('psql', [database, '-X', '-At', '-c', sql], { encoding: 'utf8' }).trim();
const pause = ms => new Promise(done => setTimeout(done, ms));
const browser = await chromium.launch({ channel: 'chrome', headless: true });
const savedSession = '/private/tmp/invoyis-issue12-browser-session.json';
const context = await browser.newContext({ ignoreHTTPSErrors: true, acceptDownloads: true,
  ...(existsSync(savedSession) ? { storageState: savedSession } : {}) });
const page = await context.newPage();
const api = async (method, path, body) => {
  const response = await context.request.fetch(`${origin}/api/v1${path}`, {
    method, headers: { Origin: origin, 'X-Invoyis-Intent': 'browser' }, data: body,
  });
  const text = await response.text();
  if (!response.ok()) throw new Error(`${method} ${path}: ${response.status()} ${text}`);
  return text ? JSON.parse(text) : null;
};
try {
  if (!existsSync(savedSession)) {
  await page.goto(`${origin}/sign-in`);
  const before = await (await fetch(`${mailpit}/api/v1/messages`)).json();
  const seen = new Set(before.messages?.map(m => m.ID) ?? []);
  await page.getByRole('textbox', { name: 'Email address' }).fill(email);
  await page.getByRole('button', { name: 'Send code' }).click();
  await page.getByRole('textbox', { name: 'Six-digit code' }).waitFor();
  let code;
  for (let i = 0; i < 50; i++) {
    const inbox = await (await fetch(`${mailpit}/api/v1/messages`)).json();
    const message = inbox.messages?.find(m => !seen.has(m.ID) && m.To.some(to => to.Address === email));
    code = message?.Snippet.match(/\b[0-9]{6}\b/)?.[0];
    if (code) break;
    await pause(200);
  }
  assert.ok(code, 'no synthetic sign-in code');
  await page.getByRole('textbox', { name: 'Six-digit code' }).fill(code);
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  await page.getByText('Signed in as').waitFor();
  await context.storageState({ path: savedSession });
  }
  await api('PUT', '/account/language', { language: 'en' });

  const archive = process.env.ISSUE12_RESUME_ARCHIVE ? { id: process.env.ISSUE12_RESUME_ARCHIVE } : await api('POST', '/archives', { kind: 'personal', name: 'Issue 12 browser' });
  let documentIds = [];
  if (!process.env.ISSUE12_RESUME_ARCHIVE) {
    await page.goto(`${origin}/archives/${archive.id}`);
    await page.getByRole('button', { name: 'Add documents' }).first().click();
    await page.locator('input[type=file]').setInputFiles([firstFile, secondFile]);
    await page.locator('[data-action=upload]').click();
    for (let i = 0; i < 120; i++) {
      const raw = query(`SELECT d.id FROM invoyis.documents d JOIN invoyis.submissions s ON s.id=d.submission_id WHERE d.archive_id='${archive.id}' ORDER BY s.original_filename`);
      documentIds = raw ? raw.split('\n') : [];
      if (documentIds.length === 2) break;
      await pause(500);
    }
  } else {
    documentIds = query(`SELECT d.id FROM invoyis.documents d JOIN invoyis.submissions s ON s.id=d.submission_id WHERE d.archive_id='${archive.id}' ORDER BY s.original_filename`).split('\n');
  }
  assert.equal(documentIds.length, 2, 'batch upload did not commit two documents');
  for (let i = 0; i < 120 && !process.env.ISSUE12_RESUME_ARCHIVE; i++) {
    const count = Number(query(`SELECT count(*) FROM invoyis.documents WHERE archive_id='${archive.id}' AND state='needs_review' AND clearance='cleared'`));
    if (count === 2) break;
    if (i === 119) throw new Error('worker did not clear both synthetic PDFs');
    await pause(500);
  }
  if (!process.env.ISSUE12_RESUME_ARCHIVE) {
    const supplier = await api('POST', `/archives/${archive.id}/organisation/suppliers`, { displayName: 'Synthetic supplier' });
    for (const documentId of documentIds) {
    await api('PUT', `/archives/${archive.id}/documents/${documentId}/review`, {
      action: 'confirm', expectedVersion: 0, idempotencyKey: crypto.randomUUID(), sourceRunId: null,
      metadata: { documentType: 'invoice', supplier: 'Printed synthetic supplier', invoiceId: '000-12',
        issueDate: '2026-11-12', currency: 'ILS', subtotal: null, tax: null, total: '100.00', taxLines: [] },
      filing: { supplierId: supplier.id, referenceScan: false }, acknowledgedWarnings: [],
      inputLocale: 'en', rawAmounts: { subtotal: null, tax: null, total: '100.00', taxLines: [] },
    });
    }
  }
  await page.goto(`${origin}/archives/${archive.id}/duplicates`);
  await page.getByRole('heading', { name: 'Likely duplicates' }).waitFor();
  await page.getByText('issue12-browser-1.pdf').waitFor();
  await page.getByText('issue12-browser-2.pdf').waitFor();
  assert.equal(await page.getByRole('button', { name: 'Duplicate', exact: true }).count(), 1);
  assert.equal(await page.getByRole('button', { name: 'Keep both' }).count(), 1);
  await page.keyboard.press('Tab');
  assert.ok(await page.evaluate(() => document.activeElement?.tagName !== 'BODY'), 'keyboard focus did not move');
  await page.getByRole('button', { name: 'Keep both' }).focus();
  assert.equal(await page.evaluate(() => document.activeElement?.textContent?.trim()), 'Keep both');
  await page.keyboard.press('Enter');
  await page.getByText('No likely matches to review.').waitFor();

  const files = [readFileSync(firstFile), readFileSync(secondFile)];
  for (let i = 0; i < documentIds.length; i++) {
    const response = await context.request.get(`${origin}/api/v1/archives/${archive.id}/documents/${documentIds[i]}/original`);
    assert.equal(response.status(), 200);
    assert.deepEqual(await response.body(), files[i]);
  }
  for (const [language, heading] of [['fr', 'Doublons possibles'], ['he', 'כפילויות אפשריות']]) {
    await api('PUT', '/account/language', { language });
    await page.reload();
    await page.getByRole('heading', { name: heading }).waitFor();
    if (language === 'he') assert.equal(await page.locator('html').getAttribute('dir'), 'rtl');
  }
  console.log(JSON.stringify({ archiveId: archive.id, documentIds, batchUploads: 2, resolution: 'keep_both', downloads: 'byte-identical', keyboard: 'focus and Enter activation', locales: ['en', 'fr', 'he'], hebrewDirection: 'rtl' }));
} finally {
  await browser.close();
}
