import assert from 'node:assert/strict';
import test from 'node:test';
import { canResolveDuplicate, resolveAndRefresh } from './duplicate-queue.mjs';

test('only correction roles receive duplicate decisions', () => {
  assert.equal(canResolveDuplicate('admin'), true);
  assert.equal(canResolveDuplicate('contributor'), true);
  assert.equal(canResolveDuplicate('viewer'), false);
  assert.equal(canResolveDuplicate(undefined), false);
});

test('resolution reloads the server page so candidate 101 replaces a resolved row', async () => {
  let visible = Array.from({ length: 100 }, (_, i) => i + 1);
  await resolveAndRefresh(async () => ({ data: { state: 'keep_both' } }), async () => {
    visible = Array.from({ length: 100 }, (_, i) => i + 2);
  });
  assert.equal(visible.length, 100);
  assert.equal(visible[0], 2);
  assert.equal(visible.at(-1), 101);
});

test('another reviewer changing a candidate triggers reload on conflict', async () => {
  let visible = [{ id: 'old', revision: 'r1' }];
  const result = await resolveAndRefresh(async () => ({ response: { status: 409 } }), async () => {
    visible = [{ id: 'old', revision: 'r2' }];
  });
  assert.equal(result.response.status, 409);
  assert.deepEqual(visible, [{ id: 'old', revision: 'r2' }]);
});
