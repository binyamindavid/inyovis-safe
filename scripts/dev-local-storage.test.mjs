import test from 'node:test';
import assert from 'node:assert/strict';
import { localStorageArgs } from './dev-local-storage.mjs';

test('local SeaweedFS only binds loopback when started', () => {
  const args = localStorageArgs({ LOCAL_S3_BINARY: '/weed', LOCAL_S3_DATA_DIR: '/data', LOCAL_S3_CONFIG: '/config' }, false, () => true);
  assert.ok(args.includes('-ip.bind=127.0.0.1'));
  assert.ok(args.includes('-s3.port=8333'));
});

test('running storage is reused even with stale local paths', () => {
  const stale = { LOCAL_S3_BINARY: '/missing/weed', LOCAL_S3_DATA_DIR: '/missing/data', LOCAL_S3_CONFIG: '/missing/config' };
  assert.equal(localStorageArgs(stale, true, () => { throw new Error('must not inspect stale paths'); }), null);
  assert.throws(() => localStorageArgs(stale, false, () => false), /must all exist/);
});
