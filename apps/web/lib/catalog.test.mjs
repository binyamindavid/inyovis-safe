import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
// TypeScript checks the catalogue values; this guards accidental key deletion in translations.
const source = readFileSync(new URL('./catalog.ts', import.meta.url), 'utf8');
test('catalogues declare the same keys', () => {
  const blocks = ['en','fr','he'].map(locale => source.match(new RegExp(`const ${locale}(?:: Copy)? = \\{([\\s\\S]*?)\\n\\}${locale === 'en' ? ' as const' : ''};`))?.[1]);
  assert.ok(blocks.every(Boolean));
  const keys = blocks.map(block => [...block.matchAll(/(?:^|[,\n])\s*([a-zA-Z][a-zA-Z0-9]*):/g)].map(match => match[1]).sort());
  assert.deepEqual(keys[1], keys[0]); assert.deepEqual(keys[2], keys[0]);
});
