// Local synthetic browser check. Requires a running Next dev server on localhost:3000.
import assert from 'node:assert/strict';
import { chromium } from 'playwright';

const executablePath = process.env.CHROME_PATH || '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome';
const browser = await chromium.launch({ headless: true, executablePath });
const archive = '11111111-1111-4111-8111-111111111111';
const document = '22222222-2222-4222-8222-222222222222';
const supplier = '44444444-4444-4444-8444-444444444444';
const ids = { categories: '55555555-5555-4555-8555-555555555555', properties: '66666666-6666-4666-8666-666666666666', 'paper-locations': '77777777-7777-4777-8777-777777777777' };
const labels = { categories: 'Énergie', properties: 'דירה א׳', 'paper-locations': 'ארון מסמכים' };
const metadata = { documentType: 'invoice', supplier: 'Café Electricité — חשמל', invoiceId: '0007', accountReference: '001-A', issueDate: '2026-11-11', currency: 'ILS', subtotal: null, tax: null, total: '10.00', taxLines: [] };
const revision = (kind, id, filing) => ({ id, kind, actorId: 1, createdAt: new Date().toISOString(), sourceRunId: null, metadata, filing, provenance: {}, acknowledgedWarnings: [] });
const baseline = revision('confirmed', '33333333-3333-4333-8333-333333333333', { referenceScan: false });
let state = { documentId: document, state: 'ready', reviewVersion: 1, sourceRunId: null, candidates: metadata, candidateWarnings: [], confirmed: baseline, draft: null, history: [baseline] };
const created = { suppliers: [{ id: supplier, displayName: 'Café Électricité', archived: false }], categories: [], properties: [], 'paper-locations': [] };
const page = await browser.newPage({ ignoreHTTPSErrors: true });
await page.route('**/api/v1/archives/**', async route => {
  const url = new URL(route.request().url());
  const path = url.pathname;
  if (path.includes('/preview/')) return route.fulfill({ status: 503, json: { code: 'PREVIEW_UNAVAILABLE' } });
  if (path.endsWith('/review')) {
    if (route.request().method() === 'GET') return route.fulfill({ json: state });
    const body = route.request().postDataJSON();
    const kind = body.action === 'draft' ? 'draft' : 'confirmed';
    const saved = revision(kind, crypto.randomUUID(), body.filing);
    state = { ...state, state: kind === 'confirmed' ? 'ready' : state.state, reviewVersion: state.reviewVersion + 1,
      confirmed: kind === 'confirmed' ? saved : state.confirmed, draft: kind === 'draft' ? saved : null, history: [saved, ...state.history] };
    return route.fulfill({ json: state });
  }
  if (path.endsWith('/supplier-suggestions')) return route.fulfill({ json: { items: [{ ...created.suppliers[0], match: 'broad' }] } });
  const match = /\/organisation\/(suppliers|categories|properties|paper-locations)(?:\/([0-9a-f-]+))?$/.exec(path);
  if (match) {
    const kind = match[1];
    if (route.request().method() === 'POST') {
      const record = { id: ids[kind], displayName: route.request().postDataJSON().displayName, archived: false };
      created[kind].push(record);
      return route.fulfill({ status: 201, json: record });
    }
    if (match[2]) return route.fulfill({ json: created[kind].find(item => item.id === match[2]) });
    return route.fulfill({ json: { items: created[kind] } });
  }
  return route.fulfill({ status: 503, json: { code: 'UNAVAILABLE' } });
});
try {
  await page.goto(`https://localhost:3000/archives/${archive}/documents/${document}/review`);
  await page.getByRole('heading', { name: 'Review document' }).waitFor();
  await page.getByRole('button', { name: 'Café Électricité · Broad' }).click();
  for (const [index, kind] of ['categories', 'properties', 'paper-locations'].entries()) {
    const label = ['Category', 'Property', 'Paper original location'][index];
    await page.getByLabel(`Add filing label: ${label}`).fill(labels[kind]);
    await page.getByRole('button', { name: 'Add filing label' }).nth(index).click();
  }
  await page.getByText('Reference scan', { exact: true }).first().click();
  await page.getByRole('button', { name: 'Save draft' }).click();
  await page.getByText('Review saved.').waitFor();
  assert.equal(state.draft.filing.supplierId, supplier);
  assert.equal(state.draft.filing.categoryId, ids.categories);
  assert.equal(state.draft.filing.paperLocationId, ids['paper-locations']);
  assert.equal(state.draft.filing.referenceScan, true);
  assert.equal(state.confirmed.id, baseline.id);
  assert.equal(state.confirmed.filing.referenceScan, false);
  await page.reload();
  await page.getByRole('heading', { name: 'Review document' }).waitFor();
  await page.getByText('Active draft; exports continue to use the confirmed revision.').waitFor();
  assert.equal(await page.getByRole('checkbox', { name: 'Reference scan' }).isChecked(), true);
  await page.getByRole('button', { name: 'Confirm review' }).click();
  await page.getByText('Review saved.').waitFor();
  assert.equal(state.confirmed.filing.supplierId, supplier);
  assert.equal(state.confirmed.filing.propertyId, ids.properties);
  assert.equal(state.confirmed.filing.referenceScan, true);
  assert.equal(state.draft, null);
  assert.equal(state.history.length, 3);
  console.log('English synthetic browser draft, reload, confirm, and history: PASS');
  for (const [locale, heading, label] of [['fr', 'Vérifier le document', 'Emplacement de l’original papier'], ['he', 'בדיקת מסמך', 'מיקום המקור הניירי']]) {
    await page.context().addCookies([{ name: 'invoyis_locale', value: locale, domain: 'localhost', path: '/' }]);
    await page.reload();
    await page.getByRole('heading', { name: heading }).waitFor();
    await page.getByText(label).first().waitFor();
    assert.equal(await page.locator('html').getAttribute('dir'), locale === 'he' ? 'rtl' : 'ltr');
    console.log(`${locale} filing labels and direction: PASS`);
  }
} finally {
  await browser.close();
}
