import * as assert from 'node:assert/strict';
import { test } from 'node:test';

import { resolveServerPath } from './serverPathResolver';

test('resolveServerPath prefers managed install when enabled', async () => {
  const result = await resolveServerPath(
    {
      externalPath: '/tmp/thriftls-external',
      managedEnabled: true,
    },
    {
      externalPathExists: () => true,
      installManaged: async () => '/tmp/thriftls-managed',
    },
  );

  assert.equal(result.source, 'managed');
  assert.equal(result.path, '/tmp/thriftls-managed');
});

test('resolveServerPath falls back to external path when managed install fails', async () => {
  const managedError = new Error('network down');
  const result = await resolveServerPath(
    {
      externalPath: '/tmp/thriftls-external',
      managedEnabled: true,
    },
    {
      externalPathExists: () => true,
      installManaged: async () => {
        throw managedError;
      },
    },
  );

  assert.equal(result.source, 'external');
  assert.equal(result.path, '/tmp/thriftls-external');
  assert.equal(result.managedError, managedError);
});

test('resolveServerPath returns none when no viable path exists', async () => {
  const result = await resolveServerPath(
    {
      externalPath: '/tmp/does-not-exist',
      managedEnabled: false,
    },
    {
      externalPathExists: () => false,
      installManaged: async () => '/tmp/unused',
    },
  );

  assert.equal(result.source, 'none');
  assert.equal(result.path, '');
});
