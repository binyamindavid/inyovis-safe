import test from 'node:test';
import assert from 'node:assert/strict';
import { FOLDER_NONE, folderCountLabel, folderTone, monogram, orderFolders } from './document-folders.mjs';
import { readFileSync } from 'node:fs';
import ts from 'typescript';

const source = readFileSync(new URL('./catalog-data.ts', import.meta.url), 'utf8');
const js = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 } }).outputText;
const { catalog } = await import(`data:text/javascript;base64,${Buffer.from(js).toString('base64')}`);

const f = (id, displayName, count) => ({ id, displayName, archived: false, count });

test('folders sort for the interface language and put the missing-metadata folder last', () => {
  const items = [f('3', 'Électricité', 2), f('1', 'water 10', 1), f('2', 'Water 9', 4), f('4', 'arnona', 1)];
  assert.deepEqual(orderFolders(items, 3, 'fr', 'Sans catégorie').map(x => x.displayName), ['arnona', 'Électricité', 'Water 9', 'water 10', 'Sans catégorie']);
  const last = orderFolders(items, 3, 'en', 'Uncategorised').at(-1);
  assert.equal(last.id, FOLDER_NONE);
  assert.equal(last.count, 3);
  assert.equal(orderFolders(items, 0, 'en', 'Uncategorised').some(x => x.id === FOLDER_NONE), false);
  assert.deepEqual(orderFolders([f('b', 'גז', 1), f('a', 'אור', 1)], 0, 'he', '').map(x => x.id), ['a', 'b']);
});

test('folder counts read naturally in each language', () => {
  for (const locale of ['en', 'fr', 'he']) {
    const words = catalog[locale];
    assert.equal(folderCountLabel(words, 0, locale), words.folderCountNone);
    assert.equal(folderCountLabel(words, 1, locale), words.folderCountOne);
    assert.match(folderCountLabel(words, 1204, locale), /1[\s,.  ]?204/);
  }
  assert.equal(folderCountLabel(catalog.en, 12, 'en'), '12 documents');
});

test('company monograms and tones are stable', () => {
  assert.equal(monogram('Israel Gas'), 'IG');
  assert.equal(monogram('ספק Café'), 'סC');
  assert.equal(monogram('Arnona'), 'AR');
  assert.equal(monogram('  — '), '');
  assert.equal(folderTone(FOLDER_NONE), 'grey');
  assert.equal(folderTone('11111111-1111-4111-8111-111111111111'), folderTone('11111111-1111-4111-8111-111111111111'));
});
