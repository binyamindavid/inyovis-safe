import test from 'node:test';
import assert from 'node:assert/strict';
import { confirmRequirements, discardedByTypeSwitch, groupOf, metadataForType, switchType } from './review-groups.mjs';

const invoiceForm = { documentType: 'invoice', supplier: 'Acme', invoiceId: '00042', accountReference: '', issueDate: '2026-11-11', currency: 'ILS', subtotal: null, tax: '0.00', total: '10.00', taxLines: [], title: '', sender: '', reference: '', notes: '' };

test('document types share one form and need only title and type', () => {
  for (const type of ['lease', 'court_correspondence', 'other']) {
    assert.equal(groupOf(type), 'document');
    assert.deepEqual(confirmRequirements(type), ['documentType', 'title']);
  }
  assert.deepEqual(confirmRequirements('credit_note'), ['documentType', 'supplier', 'issueDate', 'currency', 'total']);
});

test('type switch warns only about unsaved values that would be discarded', () => {
  const saved = { ...invoiceForm };
  assert.deepEqual(discardedByTypeSwitch(invoiceForm, saved, 'lease'), [], 'seeded or saved values need no warning');
  const edited = { ...invoiceForm, supplier: 'Typed by reviewer', total: '11.00' };
  assert.deepEqual(discardedByTypeSwitch(edited, saved, 'lease'), ['supplier', 'total']);
  assert.deepEqual(discardedByTypeSwitch(edited, saved, 'bill'), [], 'same group keeps values');
});

test('switching to a document type clears invoice values and keeps the shared date', () => {
  const next = switchType(invoiceForm, 'court_correspondence');
  assert.equal(next.supplier, '');
  assert.equal(next.tax, null);
  assert.deepEqual(next.taxLines, []);
  assert.equal(next.issueDate, '2026-11-11');
});

test('document metadata omits invoice fields and keeps references as exact text', () => {
  const form = { ...switchType(invoiceForm, 'court_correspondence'), title: 'מכתב / Court letter', sender: 'בית משפט השלום', reference: '000417/2026-ת', notes: 'דירה 5, Rothschild 1' };
  const meta = metadataForType(form);
  assert.deepEqual(Object.keys(meta).sort(), ['documentType', 'issueDate', 'notes', 'reference', 'sender', 'title']);
  assert.equal(meta.reference, '000417/2026-ת');
  assert.equal(metadataForType(invoiceForm), null);
});
