import test from 'node:test';
import assert from 'node:assert/strict';
import { formatExactMoney, formatDateOnly } from './document-format.mjs';
test('large exact amounts and zero retain every digit and currency', () => {
  assert.match(formatExactMoney('9007199254740993.01','EUR','en'), /9,007,199,254,740,993\.01 EUR/);
  assert.match(formatExactMoney('0.00','ILS','fr'), /0,00 ILS/);
  assert.throws(() => formatExactMoney('1.00','USD','en'));
});
test('date only remains January 1 in negative-offset runtime', () => {
  for (const locale of ['en','fr','he']) assert.ok(formatDateOnly('2026-01-01', locale).includes('2026'));
  assert.equal(formatDateOnly('2026-01-01','en-US'),'01/01/2026');
  assert.throws(() => formatDateOnly('2026-02-30','en'));
});
