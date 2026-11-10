import test from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { formatExactMoney, formatDateOnly } from './document-format.mjs';

test('exact EUR and ILS money preserves large, zero, and negative values', () => {
  assert.equal(formatExactMoney({ amount:'9007199254740993.01', currency:'EUR' }, 'en'), '9,007,199,254,740,993.01 EUR');
  assert.equal(formatExactMoney({ amount:'9007199254740993.01', currency:'ILS' }, 'en'), '9,007,199,254,740,993.01 ILS');
  assert.equal(formatExactMoney({ amount:'0.00', currency:'ILS' }, 'fr'), '0,00 ILS');
  assert.equal(formatExactMoney({ amount:'-0.01', currency:'EUR' }, 'en'), '-0.01 EUR');
  assert.match(formatExactMoney({ amount:'125.40', currency:'ILS' }, 'he'), /125[.,]40 ILS/);
  assert.throws(() => formatExactMoney({ amount:'-0.00', currency:'ILS' }, 'en'));
  assert.throws(() => formatExactMoney({ amount:'1.00', currency:'USD' }, 'en'));
});

test('date only remains January 1 even where UTC midnight is December 31', () => {
  const script = `import {formatDateOnly} from ${JSON.stringify(new URL('./document-format.mjs', import.meta.url).href)};
    console.log(JSON.stringify({naive:new Date('2026-01-01').toLocaleDateString('en-US'),fixed:formatDateOnly('2026-01-01','en-US')}));`;
  const utc = JSON.parse(execFileSync(process.execPath, ['--input-type=module', '-e', script], { env: { ...process.env, TZ:'UTC' }, encoding:'utf8' }));
  const west = JSON.parse(execFileSync(process.execPath, ['--input-type=module', '-e', script], { env: { ...process.env, TZ:'America/Los_Angeles' }, encoding:'utf8' }));
  assert.equal(utc.naive, '1/1/2026');
  assert.equal(west.naive, '12/31/2025');
  assert.equal(utc.fixed, '01/01/2026');
  assert.equal(west.fixed, utc.fixed);
  assert.throws(() => formatDateOnly('2026-02-30','en'));
});
