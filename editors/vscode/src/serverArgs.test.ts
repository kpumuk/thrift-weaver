import * as assert from 'node:assert/strict';
import { test } from 'node:test';
import { buildServerArgs } from './serverArgs';

test('buildServerArgs preserves explicit server args when worker count is automatic', () => {
  assert.deepEqual(buildServerArgs(['--log-level=debug'], 0), ['--log-level=debug']);
});

test('buildServerArgs appends workspace index worker flag', () => {
  assert.deepEqual(buildServerArgs(['--log-level=debug'], 3), [
    '--log-level=debug',
    '--workspace-index-workers=3',
  ]);
});
