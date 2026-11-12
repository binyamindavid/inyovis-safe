import test from 'node:test';
import assert from 'node:assert/strict';
import { documentTypeLabel, identifierIsInvoiceId, typesMatching } from './document-types.mjs';

const en = { invoice: 'Invoice', bill: 'Bill', creditNote: 'Credit note', typeLease: 'Lease', typeCourtCorrespondence: 'Court correspondence', typeOther: 'Other' };
const he = { invoice: 'חשבונית', bill: 'חשבון', creditNote: 'חשבונית זיכוי', typeLease: 'חוזה שכירות', typeCourtCorrespondence: 'התכתבות עם בית משפט', typeOther: 'אחר' };

test('labels cover bills and credit notes', () => {
  assert.equal(documentTypeLabel(en, 'bill'), 'Bill');
  assert.equal(documentTypeLabel(en, 'credit_note'), 'Credit note');
  assert.equal(documentTypeLabel(en, null), '');
  assert.equal(documentTypeLabel(en, 'receipt'), '');
});

test('search maps translated labels to types', () => {
  assert.deepEqual(typesMatching(en, 'credit', 'en'), ['credit_note']);
  assert.deepEqual(typesMatching(en, ' BILL ', 'en'), ['bill']);
  assert.deepEqual(typesMatching(he, 'חשבון', 'he'), ['bill']);
  assert.deepEqual(typesMatching(he, 'חשבונית', 'he'), ['invoice', 'credit_note']);
  assert.deepEqual(typesMatching(en, '', 'en'), []);
});

test('identifier label follows the review field groups', () => {
  for (const type of ['invoice', 'bill', 'credit_note', null]) assert.equal(identifierIsInvoiceId(type), true, String(type));
  for (const type of ['lease', 'court_correspondence', 'other']) assert.equal(identifierIsInvoiceId(type), false, type);
});
