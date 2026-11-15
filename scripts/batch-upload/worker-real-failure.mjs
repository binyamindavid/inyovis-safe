import assert from 'node:assert/strict';
import { archive, browserContext } from './config.mjs';

const { browser, context } = await browserContext();
const page = await context.newPage();
try {
  await page.goto(`https://localhost:3000/archives/${archive}`);
  await page.locator('input[type=file]').waitFor();
  await page.locator('input[type=file]').setInputFiles({
    name: 'synthetic-invalid-pdf.pdf', mimeType: 'application/pdf',
    buffer: Buffer.from('%PDF-1.7\nnot a valid PDF body\n%%EOF'),
  });
  const row = page.locator('[data-upload-queue] > li').first();
  await row.and(page.locator('[data-receipt-id]')).waitFor({ state: 'attached', timeout: 20000 });
  await row.getByText('Processing failed').waitFor({ timeout: 90000 });
  assert.ok(await row.getAttribute('data-receipt-id'));
  assert.equal(await row.getByRole('button', { name: /Retry/ }).count(), 0);
  console.log(JSON.stringify({ case: 'real-worker-invalid-pdf', browser: browser.version(), receiptRetained: true, uploadRetry: false, result: 'pass' }));
} finally { await browser.close(); }
