import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { chromium } from 'playwright';

const root = resolve(fileURLToPath(new URL('../..', import.meta.url)));
export const archive = process.env.BATCH_TEST_ARCHIVE_ID;
export const storageState = process.env.BATCH_TEST_STORAGE_STATE;
assert.match(archive ?? '', /^[0-9a-f-]{36}$/i, 'Set BATCH_TEST_ARCHIVE_ID to a synthetic local archive');
assert.ok(storageState, 'Set BATCH_TEST_STORAGE_STATE to a local synthetic account session');
assert.match(process.env.DATABASE_URL ?? '', /@127\.0\.0\.1:5432\/invoyis_safe_dev\b/, 'Use the disposable local development database');
export const lease = readFileSync(resolve(root, '.local-bin/issue35-fixtures/lease.pdf'));
export const court = readFileSync(resolve(root, '.local-bin/issue35-fixtures/court.pdf'));
export const other = readFileSync(resolve(root, '.local-bin/issue35-fixtures/other.pdf'));
export const pdf = (name, buffer) => ({ name, mimeType: 'application/pdf', buffer });
export const sql = query => execFileSync('psql', [process.env.DATABASE_URL, '-v', 'ON_ERROR_STOP=1', '-Atc', query], { encoding: 'utf8' }).trim();
export async function browserContext() {
  const browser = await chromium.launch({ channel: 'chrome', headless: true });
  const context = await browser.newContext({ ignoreHTTPSErrors: true, storageState });
  const language = await context.request.put('https://localhost:3000/api/v1/account/language', { headers: { Origin: 'https://localhost:3000', 'X-Invoyis-Intent': 'browser' }, data: { language: 'en' } });
  assert.equal(language.status(), 200);
  return { browser, context };
}
export async function open(context) {
  const page = await context.newPage();
  await page.goto(`https://localhost:3000/archives/${archive}`);
  await page.locator('input[type=file]').waitFor();
  return page;
}
