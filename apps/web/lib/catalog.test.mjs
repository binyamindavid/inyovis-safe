import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import ts from 'typescript';

const source = readFileSync(new URL('./catalog-data.ts', import.meta.url), 'utf8');
const js = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 } }).outputText;
const { catalog } = await import(`data:text/javascript;base64,${Buffer.from(js).toString('base64')}`);

test('catalogues declare the same nonempty keys', () => {
  const english = Object.keys(catalog.en).sort();
  for (const language of ['fr', 'he']) {
    assert.deepEqual(Object.keys(catalog[language]).sort(), english);
    for (const key of english) assert.ok(catalog[language][key].trim(), `${language}.${key}`);
  }
});
