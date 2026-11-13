import assert from 'node:assert/strict';
import { mkdirSync } from 'node:fs';
import { chromium } from 'playwright';
// Browser checks for archive folders (#59) against a mocked archive API (no Go, database or storage needed).
// Run while the web app is served (`yarn dev:local`, or `yarn build && yarn workspace @invoyis/web start`).
// BASE_URL defaults to the dev:local HTTPS origin; CHROME_PATH defaults to macOS Chrome. SCREENSHOTS=dir also writes
// the PR screenshots there.
const base = process.env.BASE_URL || 'https://localhost:3000';
const host = new URL(base).hostname;
const executablePath = process.env.CHROME_PATH || '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome';
const shots = process.env.SCREENSHOTS;
if (shots) mkdirSync(shots, { recursive: true });
const browser = await chromium.launch({ headless: true, executablePath });

const A = '11111111-1111-4111-8111-111111111111', B = '88888888-8888-4888-8888-888888888888';
const id = (prefix, n) => `${prefix}${String(n).padStart(7, '0')}-0000-4000-8000-000000000000`;
const suppliers = [
  { id: id('1', 1), displayName: 'Israel Gas' }, { id: id('1', 2), displayName: 'חברת החשמל לישראל' }, { id: id('1', 3), displayName: 'Water Co' },
  { id: id('1', 4), displayName: 'Société Générale d’Assurances et de Prévoyance des Travailleurs Indépendants de la Région' },
  { id: id('1', 5), displayName: 'Bezeq' }, { id: id('1', 6), displayName: 'Tel Aviv-Yafo Municipality' }, { id: id('1', 7), displayName: 'Old Landlord', archived: true },
];
const categories = [
  { id: id('2', 1), displayName: 'Utilities' }, { id: id('2', 2), displayName: 'Insurance' }, { id: id('2', 3), displayName: 'Municipal tax' },
  { id: id('2', 4), displayName: 'Telecoms' }, { id: id('2', 5), displayName: 'Rent' }, { id: id('2', 6), displayName: 'Car' },
];
const foreignSupplier = id('9', 1);
const [gas, electric, water, insurer, bezeq, city, landlord] = suppliers;
const [utilities, insurance, municipal, telecoms, rent] = categories;

function makeDocs() {
  const docs = []; let n = 0;
  const add = (supplier, category, extra = {}) => { n++; docs.push({ id: id('4', n), archive: A, status: 'ready', originalFilename: `scan-${n}.pdf`, confirmedType: 'invoice', issueDate: `2026-${String(1 + (n % 9)).padStart(2, '0')}-${String(1 + (n % 27)).padStart(2, '0')}`, printedSupplier: supplier?.displayName ?? null, documentNumber: `INV-${String(n).padStart(4, '0')}`, total: `${(n * 37) % 900}.40`, currency: 'ILS', supplierId: supplier?.id ?? null, supplierName: supplier?.displayName ?? null, categoryId: category?.id ?? null, categoryName: category?.displayName ?? null, referenceScan: false, ...extra }); };
  // Israel Gas spans two list pages (25 documents) and is also filed under Utilities.
  for (let i = 0; i < 25; i++) add(gas, utilities);
  for (let i = 0; i < 7; i++) add(electric, utilities);
  for (let i = 0; i < 4; i++) add(water, utilities);
  for (let i = 0; i < 3; i++) add(insurer, insurance);
  for (let i = 0; i < 5; i++) add(bezeq, telecoms);
  for (let i = 0; i < 6; i++) add(city, municipal, { confirmedType: 'bill' });
  add(landlord, rent, { confirmedType: 'lease', confirmedTitle: 'Lease 2024', documentNumber: null, total: null, currency: null });
  // Missing metadata: a confirmed note with neither, a bill with no category, and two uploads awaiting review.
  add(null, null, { confirmedType: 'other', confirmedTitle: 'Warranty card', documentNumber: null, total: null, currency: null });
  add(water, null, { confirmedType: 'bill' });
  n++; docs.push({ id: id('4', n), archive: A, status: 'needs_review', originalFilename: 'IMG_2041.pdf', referenceScan: false });
  n++; docs.push({ id: id('4', n), archive: A, status: 'processing', originalFilename: 'electricity-october.pdf', referenceScan: false });
  docs.push({ id: id('5', 1), archive: B, status: 'ready', originalFilename: 'b.pdf', confirmedType: 'invoice', issueDate: '2026-01-01', supplierId: foreignSupplier, supplierName: 'Foreign', referenceScan: false });
  return docs;
}

async function invoicePng(page) {
  await page.setViewportSize({ width: 560, height: 740 });
  await page.setContent(`<body style="margin:0;font:13px system-ui;color:#1b2333;background:#fff"><div style="padding:40px 44px">
    <div style="display:flex;justify-content:space-between;align-items:center"><div style="font-size:22px;font-weight:600;color:#2f57c0">Israel Gas</div><div style="color:#5d6779">Invoice INV-0001</div></div>
    <div style="margin-top:28px;color:#5d6779">Billed to</div><div>Pilot Personal · Account 00 1-A</div>
    <table style="margin-top:36px;width:100%;border-collapse:collapse">${['Gas supply · September', 'Meter rental', 'Service fee'].map((r, i) => `<tr style="border-bottom:1px solid #e7e9ef"><td style="padding:10px 0">${r}</td><td style="text-align:right">${[214.1, 12, 7.3][i].toFixed(2)} ₪</td></tr>`).join('')}</table>
    <div style="margin-top:20px;text-align:right;font-size:16px;font-weight:600">Total 233.40 ₪</div>
    <div style="margin-top:120px;height:60px;background:repeating-linear-gradient(90deg,#1b2333 0 2px,#fff 2px 5px);width:220px"></div></div></body>`);
  return page.screenshot({ type: 'png' });
}

function mockApi(page, { docs, log, png }) {
  const archives = [{ id: A, name: 'Pilot Personal', kind: 'personal', role: 'admin', jurisdiction: null, canBulkExport: true }, { id: B, name: 'Studio Business', kind: 'business', role: 'viewer', jurisdiction: 'IL', canBulkExport: false }];
  const pageOf = (rows, cursor) => { const start = cursor ? Number(cursor) : 0; return { items: rows.slice(start, start + 20), nextCursor: start + 20 < rows.length ? String(start + 20) : '' }; };
  const cleared = d => d.status === 'ready' || d.status === 'needs_review';
  return page.route('**/api/v1/archives**', async route => {
    const u = new URL(route.request().url()); const p = u.pathname; log.push(`${route.request().method()} ${p}?${u.searchParams}`);
    if (p === '/api/v1/archives') return route.fulfill({ json: { items: archives } });
    const a = archives.find(x => p.startsWith(`/api/v1/archives/${x.id}`)); if (!a) return route.fulfill({ status: 404, json: { code: 'NOT_FOUND' } });
    const rest = p.slice(`/api/v1/archives/${a.id}`.length); const mine = docs.filter(d => d.archive === a.id);
    const own = { suppliers: a.id === A ? suppliers : [{ id: foreignSupplier, displayName: 'Foreign' }], categories: a.id === A ? categories : [] };
    if (rest === '') return route.fulfill({ json: a });
    if (rest === '/document-counts') return route.fulfill({ json: { total: mine.length, needsReview: mine.filter(d => d.status === 'needs_review').length, attention: mine.filter(d => d.status !== 'ready').length } });
    if (rest === '/document-folders') {
      const by = u.searchParams.get('by'); const key = by === 'company' ? 'supplierId' : 'categoryId';
      if (!['company', 'category'].includes(by) || [...u.searchParams.keys()].length !== 1) return route.fulfill({ status: 422, json: { code: 'VALIDATION_FAILED' } });
      const folderOf = d => cleared(d) ? d[key] ?? null : null;
      const items = own[by === 'company' ? 'suppliers' : 'categories'].map(f => ({ id: f.id, displayName: f.displayName, archived: Boolean(f.archived), count: mine.filter(d => folderOf(d) === f.id).length })).filter(f => f.count || !f.archived);
      return route.fulfill({ json: { by, items, unassigned: mine.filter(d => folderOf(d) === null).length, total: mine.length } });
    }
    if (rest.startsWith('/organisation/')) { const kind = rest.split('/').at(-1); return route.fulfill({ json: { items: kind === 'categories' ? own.categories : [] } }); }
    if (rest.includes('/preview/')) return route.fulfill({ status: 200, body: png, headers: { 'content-type': 'image/png', 'x-page-count': '1' } });
    const status = rest.match(/^\/documents\/([^/]+)\/status$/); if (status) { const d = mine.find(x => x.id === status[1]); return d ? route.fulfill({ json: d }) : route.fulfill({ status: 404, json: { code: 'NOT_FOUND' } }); }
    if (rest.endsWith('/documents')) {
      const s = u.searchParams; let rows = mine;
      for (const [param, field, list] of [['supplierId', 'supplierId', own.suppliers], ['categoryId', 'categoryId', own.categories]]) {
        const v = s.get(param); if (!v) continue;
        if (v !== 'none' && !list.some(f => f.id === v)) return route.fulfill({ status: 404, json: { code: 'NOT_FOUND' } });
        rows = rows.filter(d => v === 'none' ? !cleared(d) || !d[field] : cleared(d) && d[field] === v);
      }
      const st = s.get('status'); if (st === 'ready') rows = rows.filter(d => d.status === 'ready'); if (st === 'attention') rows = rows.filter(d => d.status !== 'ready'); if (st === 'needs_review') rows = rows.filter(d => d.status === 'needs_review');
      const q = s.get('q')?.toLowerCase(); if (q) rows = rows.filter(d => [d.originalFilename, d.confirmedTitle, d.printedSupplier, d.documentNumber, d.supplierName].some(v => v && v.toLowerCase().includes(q)));
      rows = [...rows].sort((x, y) => (y.issueDate ?? '').localeCompare(x.issueDate ?? '') || y.id.localeCompare(x.id));
      return route.fulfill({ json: pageOf(rows, s.get('cursor')) });
    }
    return route.fulfill({ status: 404, json: { code: 'NOT_FOUND' } });
  });
}

const words = {
  en: { company: 'By company', category: 'By category', all: 'All documents', unassigned: 'Unassigned company', uncategorised: 'Uncategorised', gasCount: '25 documents', more: 'More documents', searchIn: 'Search in Israel Gas', scope: 'In Israel Gas', everywhere: 'Search all documents instead', notFound: /isn’t in this archive/, review: 'Needs review', crumbs: 'Folder path', grouping: 'Show documents' },
  fr: { company: 'Par société', category: 'Par catégorie', all: 'Tous les documents', unassigned: 'Société non attribuée', uncategorised: 'Sans catégorie', gasCount: '25 documents', more: 'Autres documents', searchIn: 'Rechercher dans Israel Gas', scope: 'Dans Israel Gas', everywhere: 'Rechercher plutôt dans tous les documents', notFound: /ne fait pas partie de cette archive/, review: 'À vérifier', crumbs: 'Chemin du dossier', grouping: 'Afficher les documents' },
  he: { company: 'לפי חברה', category: 'לפי קטגוריה', all: 'כל המסמכים', unassigned: 'ללא חברה', uncategorised: 'ללא קטגוריה', gasCount: '25 מסמכים', more: 'מסמכים נוספים', searchIn: 'חיפוש ב־Israel Gas', scope: 'בתוך Israel Gas', everywhere: 'חיפוש בכל המסמכים במקום זאת', notFound: /לא נמצאת בארכיון הזה/, review: 'דורש בדיקה', crumbs: 'נתיב התיקייה', grouping: 'הצגת מסמכים' },
};
const tile = (page, name) => page.locator('a[data-folder]', { hasText: name });
const rows = page => page.locator('ul li button[id^=row]');
const shot = async (page, name) => { if (shots) await page.screenshot({ path: `${shots}/${name}.png`, fullPage: false }); };

let png;
{ const p = await browser.newPage(); png = await invoicePng(p); await p.close(); }
try {
  for (const locale of ['en', 'fr', 'he']) {
    const w = words[locale]; const log = []; const docs = makeDocs();
    const context = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1440, height: 900 }, deviceScaleFactor: shots ? 2 : 1 });
    await context.addCookies([{ name: 'invoyis_locale', value: locale, domain: host, path: '/' }]);
    const page = await context.newPage(); await mockApi(page, { docs, log, png });
    const errors = []; page.on('pageerror', e => errors.push(e.message));

    // By company is the default view: one tile per supplier identity plus Unassigned company, with archive-wide counts.
    await page.goto(`${base}/archives/${A}`);
    await tile(page, 'Israel Gas').waitFor();
    assert.equal(await page.locator('html').getAttribute('dir'), locale === 'he' ? 'rtl' : 'ltr');
    assert.equal(await page.getByRole('group', { name: w.grouping }).getByRole('button', { name: w.company }).getAttribute('aria-pressed'), 'true');
    assert.match(await tile(page, 'Israel Gas').innerText(), new RegExp(w.gasCount));
    assert.equal(await tile(page, w.unassigned).count(), 1);
    assert.equal(await page.locator('a[data-folder]').last().getAttribute('data-folder'), 'none');
    assert.equal(await tile(page, 'Old Landlord').count(), 1, 'archived supplier with documents stays');
    assert(log.some(l => l.includes('document-folders?by=company')));
    assert(!log.some(l => /\/documents\?/.test(l)), 'the grid does not fetch the document list');
    // Long names wrap to two lines inside the tile instead of widening it.
    const longTile = await tile(page, 'Société Générale').boundingBox(); const firstTile = await tile(page, 'Israel Gas').boundingBox();
    assert(Math.abs(longTile.width - firstTile.width) < 2);
    await shot(page, `company-grid-${locale}`);

    // Keyboard: Tab reaches the tiles with a visible focus ring, arrows move between them, Enter opens.
    await tile(page, 'Israel Gas').focus();
    assert.notEqual(await tile(page, 'Israel Gas').evaluate(el => getComputedStyle(el).boxShadow), 'none');
    await page.keyboard.press(locale === 'he' ? 'ArrowLeft' : 'ArrowRight');
    assert.notEqual(await page.evaluate(() => document.activeElement?.getAttribute('data-folder')), gas.id);
    await page.keyboard.press(locale === 'he' ? 'ArrowRight' : 'ArrowLeft');
    assert.equal(await page.evaluate(() => document.activeElement?.getAttribute('data-folder')), gas.id);
    await page.keyboard.press('Enter');

    // The open folder lists that company's documents in the existing table, across pages, with a breadcrumb.
    await page.waitForURL(u => u.search.includes(`folder=${gas.id}`));
    const crumbs = page.getByRole('navigation', { name: w.crumbs });
    await crumbs.waitFor();
    assert.match(await crumbs.innerText(), /Pilot Personal[\s\S]*Israel Gas/);
    assert.match(await crumbs.innerText(), new RegExp(w.company));
    await rows(page).first().waitFor();
    assert.equal(await rows(page).count(), 20);
    assert(log.some(l => l.includes(`supplierId=${gas.id}`) && l.includes('status=ready')));
    await page.getByRole('button', { name: w.more }).click();
    await page.waitForFunction(() => document.querySelectorAll('ul li button[id^=row]').length === 25);
    assert.equal(await page.getByRole('button', { name: w.more }).count(), 0);

    // Selecting a row opens the existing preview with Review/Edit and Download.
    const first = await rows(page).first().getAttribute('id');
    await rows(page).first().click();
    await page.locator('#detail-title').waitFor();
    await page.locator('img[alt]').first().waitFor();
    assert.equal(await page.locator(`a[href$="/documents/${first.slice(4)}/original"]`).count(), 1);
    await shot(page, `company-folder-preview-${locale}`);
    // Reload keeps the folder and the open document.
    await page.reload(); await page.locator('#detail-title').waitFor(); await crumbs.waitFor();
    assert.match(page.url(), new RegExp(`folder=${gas.id}`));

    // Esc in the preview closes it (its history entry); scoped search says it is scoped and offers the archive-wide search.
    await page.locator('#detail-title').focus(); await page.keyboard.press('Escape');
    await page.waitForURL(u => !u.search.includes('doc='));
    const search = page.locator('input[type=search]');
    assert.equal(await search.getAttribute('placeholder'), w.searchIn);
    await search.fill('INV-000');
    await page.getByText(w.scope, { exact: true }).waitFor();
    await page.waitForFunction(() => document.querySelectorAll('ul li button[id^=row]').length === 9);
    assert(log.some(l => l.includes(`supplierId=${gas.id}`) && l.includes('q=INV-000')));
    await page.getByRole('button', { name: w.everywhere }).click();
    await page.waitForURL(u => !u.search.includes('folder='));
    await page.waitForFunction(() => document.querySelectorAll('ul li button[id^=row]').length === 9);
    assert(log.some(l => l.includes('q=INV-000') && !l.includes('supplierId')));
    // Browser Back returns to the folder (with its search); Back again to the folder grid with focus on the folder's tile.
    await page.goBack(); await crumbs.waitFor(); assert.match(page.url(), new RegExp(`folder=${gas.id}`));
    await search.fill('');
    await page.goBack(); await tile(page, 'Israel Gas').waitFor();
    await page.waitForFunction(id => document.activeElement?.getAttribute('data-folder') === id, gas.id);

    // Archive-wide search from the grid lists matches from every folder.
    await search.fill('warranty'); await page.waitForFunction(() => document.querySelectorAll('ul li button[id^=row]').length === 1);
    assert.match(await rows(page).first().innerText(), /Warranty/); await search.fill(''); await tile(page, 'Israel Gas').waitFor();

    // Missing metadata: Unassigned company holds the confirmed note and both uploads that are not confirmed yet.
    await tile(page, w.unassigned).click(); await page.waitForURL(u => u.search.includes('folder=none'));
    await page.waitForFunction(() => document.querySelectorAll('ul li button[id^=row]').length === 3);
    assert(log.some(l => l.includes('supplierId=none')));
    await page.getByRole('navigation', { name: w.crumbs }).getByRole('link', { name: w.company }).click();
    await tile(page, 'Israel Gas').waitFor();
    // The breadcrumb clears folder filters as well as the folder, restoring the grid.
    await page.goto(`${base}/archives/${A}?folder=${gas.id}&from=2026-01-01`);
    await crumbs.waitFor();
    await crumbs.getByRole('link', { name: w.company }).click();
    await tile(page, 'Israel Gas').waitFor();
    assert(!page.url().includes('from='));
    assert.equal(await rows(page).count(), 0);

    // By category: the same invoice opens from Utilities; Uncategorised collects the rest.
    await page.getByRole('group', { name: w.grouping }).getByRole('button', { name: w.category }).click();
    await tile(page, 'Utilities').waitFor(); await page.waitForURL(u => u.search.includes('view=category'));
    assert.match(await tile(page, w.uncategorised).innerText(), /4/);
    assert.match(await tile(page, 'Utilities').innerText(), /36/);
    await shot(page, `category-grid-${locale}`);
    await tile(page, 'Utilities').click(); await rows(page).first().waitFor();
    assert(log.some(l => l.includes(`categoryId=${utilities.id}`)));
    const gasDoc = docs.filter(d => d.supplierId === gas.id).sort((x, y) => y.issueDate.localeCompare(x.issueDate) || y.id.localeCompare(x.id))[0];
    await page.locator('input[type=search]').fill(gasDoc.documentNumber);
    await page.locator(`#row-${gasDoc.id}`).click(); await page.locator('#detail-title').waitFor();
    assert.match(page.url(), new RegExp(`view=category.*folder=${utilities.id}.*doc=${gasDoc.id}`));
    await shot(page, `category-folder-preview-${locale}`);

    // Needs review and Likely duplicates stay in the sidebar.
    const sidebar = page.locator('aside');
    await page.locator('input[type=search]').fill('');
    await sidebar.getByRole('button', { name: new RegExp(w.review) }).click(); await page.waitForURL(u => u.search.includes('view=review'));
    await page.waitForFunction(() => document.querySelectorAll('ul li button[id^=row]').length === 1);
    assert.equal(await sidebar.locator(`a[href="/archives/${A}/duplicates"]`).count(), 1);
    // All documents keeps the flat table.
    await page.getByRole('group', { name: w.grouping }).getByRole('button', { name: w.all }).click();
    await page.waitForFunction(() => document.querySelectorAll('ul li button[id^=row]').length === 22);

    // A folder URL from another archive is refused in this archive, and its documents are never listed.
    await page.goto(`${base}/archives/${A}?folder=${foreignSupplier}`);
    await page.getByText(w.notFound).waitFor();
    assert.equal(await rows(page).count(), 0);
    assert(!log.some(l => l.includes(`supplierId=${foreignSupplier}`)), 'unknown folders are not listed');
    // Malformed folder parameters fall back to the grid.
    await page.goto(`${base}/archives/${A}?folder=../../etc`); await tile(page, 'Israel Gas').waitFor();
    assert.deepEqual(errors, []);
    await context.close();
  }

  // A response whose headers arrive first must not replace a newer grouping after its body parses.
  {
    const context = await browser.newContext({ ignoreHTTPSErrors: true });
    const page = await context.newPage();
    await page.addInitScript(() => {
      const originalFetch = window.fetch.bind(window);
      window.fetch = async (...args) => {
        const response = await originalFetch(...args);
        if (!String(args[0]).includes('/document-folders?by=company')) return response;
        const originalJson = response.json.bind(response);
        response.json = async () => {
          window.companyBodyPending = true;
          await new Promise(resolve => { window.releaseCompanyBody = resolve; });
          return originalJson();
        };
        return response;
      };
    });
    await mockApi(page, { docs: makeDocs(), log: [], png });
    await page.goto(`${base}/archives/${A}`);
    await page.waitForFunction(() => window.companyBodyPending === true);
    await page.getByRole('group', { name: words.en.grouping }).getByRole('button', { name: words.en.category }).click();
    await tile(page, 'Utilities').waitFor();
    await page.evaluate(() => window.releaseCompanyBody());
    await page.waitForTimeout(100);
    assert.equal(await tile(page, 'Utilities').count(), 1, 'late company body cannot hide the category grid');
    assert.equal(await page.getByRole('status', { name: /loading/i }).count(), 0);
    await context.close();
  }

  // Mobile: two-column tiles with comfortable targets, folder list, then the existing full-screen preview.
  for (const locale of ['en', 'he']) {
    const w = words[locale]; const log = []; const docs = makeDocs();
    const context = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 390, height: 844 }, deviceScaleFactor: shots ? 3 : 1, isMobile: true, hasTouch: true });
    await context.addCookies([{ name: 'invoyis_locale', value: locale, domain: host, path: '/' }]);
    const page = await context.newPage(); await mockApi(page, { docs, log, png });
    await page.goto(`${base}/archives/${A}`); await tile(page, 'Israel Gas').waitFor();
    const box = await tile(page, 'Israel Gas').boundingBox();
    assert(box.width >= 96 && box.height >= 96, `tile ${box.width}x${box.height}`);
    assert(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), 'no horizontal scroll');
    await shot(page, `mobile-company-grid-${locale}`);
    await tile(page, 'Israel Gas').tap(); await rows(page).first().waitFor();
    await shot(page, `mobile-company-folder-${locale}`);
    await rows(page).first().tap(); await page.locator('#detail-title').waitFor();
    await page.locator('img[alt]').first().waitFor();
    await shot(page, `mobile-preview-${locale}`);
    await page.goBack(); await rows(page).first().waitFor();
    await page.goBack(); await tile(page, 'Israel Gas').waitFor();
    await context.close();
  }
  console.log('archive folders: ok');
} finally { await browser.close(); }
