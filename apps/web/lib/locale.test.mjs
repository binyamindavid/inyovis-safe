import test from 'node:test';
import assert from 'node:assert/strict';
import { resolveLocale, firstAcceptedLocale } from './locale.mjs';
test('account preference overrides explicit choice and Accept-Language', () => {
  assert.equal(resolveLocale({ account:'he-IL', choice:'fr', acceptLanguage:'en-US' }), 'he');
});
test('signed-out explicit choice and weighted supported languages', () => {
  assert.equal(resolveLocale({ choice:'fr-FR', acceptLanguage:'he-IL,en;q=.8' }), 'fr');
  assert.equal(firstAcceptedLocale('de-DE,he-IL;q=0.5,fr-FR;q=0.9'), 'fr');
  assert.equal(firstAcceptedLocale('he; q=0.9, fr;q=0.5'), 'he');
  assert.equal(resolveLocale({ acceptLanguage:'de-DE,he-IL,en-US' }), 'he');
  assert.equal(resolveLocale({ acceptLanguage:'de-DE' }), 'en');
});
