import test from 'node:test';
import assert from 'node:assert/strict';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { PrintedIdentifier } from '../components/printed-identifier.mjs';

test('printed identifier renders all characters inside an LTR isolation boundary', () => {
  const html = renderToStaticMarkup(createElement('div', { dir: 'rtl' }, createElement(PrintedIdentifier, { value: '000042-A/26' })));
  assert.match(html, /<bdi[^>]*dir="ltr"[^>]*>000042-A\/26<\/bdi>/);
});
