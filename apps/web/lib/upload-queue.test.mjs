import test from 'node:test';
import assert from 'node:assert/strict';
import { queueReducer, validateSelection, classifyFailure, MAX_FILES } from './upload-queue.mjs';
const file = { name: 'חשבונית invoice.pdf', size: 20, type: 'application/pdf' };
const row = { id: 'one', file, state: 'queued', key: 'key-one' };
test('row transitions are pure and reject illegal transitions', () => {
  const a = queueReducer([], { type: 'add', rows: [row] });
  const b = queueReducer(a, { type: 'transition', id: 'one', state: 'validating' });
  assert.equal(a[0].state, 'queued'); assert.equal(b[0].state, 'validating');
  assert.throws(() => queueReducer(b, { type: 'transition', id: 'one', state: 'received' }));
  const c = queueReducer(b, { type: 'transition', id: 'one', state: 'rejected' });
  assert.equal(c[0].key, 'key-one');
  assert.deepEqual(queueReducer(c, { type: 'processing', id: 'one', status: 'ready' }), c);
});
test('selection caps and per-file validation retain rejected rows', () => {
  assert.deepEqual(validateSelection([file, { ...file, name: 'bad.txt' }], [], 25), [null, 'file']);
  assert.equal(validateSelection([file], Array(MAX_FILES).fill(row), 25)[0], 'count');
});
test('error classification preserves the retry boundary', () => {
  assert.equal(classifyFailure(409, 'UPLOAD_STAGED_OBJECT_MISSING', 'complete'), 'missing');
  assert.equal(classifyFailure(409, 'CONFLICT', 'complete'), 'terminal');
  assert.equal(classifyFailure(422, 'VALIDATION_ERROR', 'session'), 'rejected');
  assert.equal(classifyFailure(403, '', 'transfer'), 'grant');
  assert.equal(classifyFailure(403, '', 'complete'), 'pause');
  assert.equal(classifyFailure(429, '', 'session'), 'rate');
  assert.equal(classifyFailure(500, '', 'complete'), 'recoverable');
});
