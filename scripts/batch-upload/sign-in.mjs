import assert from 'node:assert/strict';
import { chmodSync } from 'node:fs';
import { chromium } from 'playwright';
import { storageState } from './config.mjs';
const email = process.env.BATCH_TEST_EMAIL;
assert.match(email ?? '', /^[a-z0-9+._-]+@example\.test$/i, 'Set BATCH_TEST_EMAIL to an eligible synthetic account');
const browser = await chromium.launch({ channel: 'chrome', headless: true });
const context = await browser.newContext({ ignoreHTTPSErrors: true });
const page = await context.newPage();
try {
  await page.goto('https://localhost:3000/sign-in');
  const before = await (await fetch('http://127.0.0.1:8025/api/v1/messages')).json();
  const previous = new Set(before.messages?.map(item => item.ID) ?? []);
  await page.getByRole('textbox', { name: 'Email address' }).fill(email);
  await page.getByRole('button', { name: 'Send code' }).click();
  await page.getByRole('textbox', { name: 'Six-digit code' }).waitFor();
  let code;
  for (let i = 0; i < 30; i++) {
    const inbox = await (await fetch('http://127.0.0.1:8025/api/v1/messages')).json();
    const message = inbox.messages?.find(item => !previous.has(item.ID) && item.To.some(recipient => recipient.Address === email));
    code = message?.Snippet.match(/\b[0-9]{6}\b/)?.[0];
    if (code) break;
    await new Promise(resolve => setTimeout(resolve, 200));
  }
  assert.ok(code, 'Mailpit did not receive a synthetic code');
  await page.getByRole('textbox', { name: 'Six-digit code' }).fill(code);
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  await page.getByText('Signed in as').waitFor();
  await context.storageState({ path: storageState });
  chmodSync(storageState, 0o600);
  console.log('Saved temporary local synthetic browser session');
} finally { await browser.close(); }
