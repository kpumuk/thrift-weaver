import * as assert from 'node:assert/strict';
import * as crypto from 'node:crypto';
import * as fs from 'node:fs/promises';
import * as os from 'node:os';
import * as path from 'node:path';
import { test } from 'node:test';

import * as tar from 'tar';

import {
  assertSafeArchiveEntryPath,
  installManagedThriftls,
  parseManifestJSON,
  selectPlatformArtifact,
  type Manifest,
} from './managedInstall';

function sha256Hex(data: Uint8Array): string {
  return crypto.createHash('sha256').update(data).digest('hex');
}

function manifestJSON(version: string, platform: { os: string; arch: string; url: string; sha: string; size: number; filename: string }): string {
  return JSON.stringify({
    schema_version: 1,
    tool: 'thriftls',
    version,
    platforms: [
      {
        os: platform.os,
        arch: platform.arch,
        url: platform.url,
        sha256: platform.sha,
        size_bytes: platform.size,
        filename: platform.filename,
      },
    ],
  });
}

async function createTarGzBinaryArchive(binaryName: string, binaryContents: string): Promise<Uint8Array> {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), 'thrift-weaver-test-tar-'));
  const payloadDir = path.join(root, 'payload');
  const outPath = path.join(root, 'archive.tar.gz');
  try {
    await fs.mkdir(path.join(payloadDir, 'bin'), { recursive: true });
    const fullBinPath = path.join(payloadDir, 'bin', binaryName);
    await fs.writeFile(fullBinPath, binaryContents, { mode: 0o755 });

    await tar.c({ gzip: true, cwd: payloadDir, file: outPath }, ['bin']);
    return await fs.readFile(outPath);
  } finally {
    await fs.rm(root, { recursive: true, force: true });
  }
}

test('parseManifestJSON validates schema and maps fields', () => {
  const parsed = parseManifestJSON(
    JSON.stringify({
      schema_version: 1,
      tool: 'thriftls',
      version: '1.2.3',
      platforms: [
        {
          os: 'linux',
          arch: 'amd64',
          url: 'https://example.invalid/thriftls.tar.gz',
          sha256: 'a'.repeat(64),
          size_bytes: 123,
          filename: 'thriftls_1.2.3_linux_amd64.tar.gz',
        },
      ],
    }),
  );

  assert.equal(parsed.schemaVersion, 1);
  assert.equal(parsed.version, '1.2.3');
  assert.equal(parsed.platforms[0].sizeBytes, 123);
});

test('selectPlatformArtifact is deterministic for duplicate matches', () => {
  const manifest: Manifest = {
    schemaVersion: 1,
    tool: 'thriftls',
    version: '1.0.0',
    platforms: [
      {
        os: 'linux',
        arch: 'amd64',
        url: 'https://example.invalid/b.tar.gz',
        sha256: 'b'.repeat(64),
        sizeBytes: 11,
        filename: 'b.tar.gz',
      },
      {
        os: 'linux',
        arch: 'amd64',
        url: 'https://example.invalid/a.tar.gz',
        sha256: 'a'.repeat(64),
        sizeBytes: 10,
        filename: 'a.tar.gz',
      },
    ],
  };

  const selected = selectPlatformArtifact(manifest, { os: 'linux', arch: 'amd64' });
  assert.equal(selected.filename, 'a.tar.gz');
});

test('installManagedThriftls installs and returns current binary', async () => {
  const storageDir = await fs.mkdtemp(path.join(os.tmpdir(), 'thrift-weaver-test-install-'));
  const archive = await createTarGzBinaryArchive('thriftls', '#!/bin/sh\necho ok\n');
  const archiveSHA = sha256Hex(archive);

  const manifestURL = 'https://example.invalid/manifest.json';
  const archiveURL = 'https://example.invalid/thriftls_1.0.0_linux_amd64.tar.gz';
  const manifest = manifestJSON('1.0.0', {
    os: 'linux',
    arch: 'amd64',
    url: archiveURL,
    sha: archiveSHA,
    size: archive.byteLength,
    filename: 'thriftls_1.0.0_linux_amd64.tar.gz',
  });

  const data = new Map<string, Uint8Array>([
    [manifestURL, Buffer.from(manifest, 'utf8')],
    [archiveURL, archive],
  ]);
  const fetchFromMap = async (url: string): Promise<Uint8Array> => {
    const value = data.get(url);
    assert.ok(value, `unexpected URL ${url}`);
    return value;
  };

  try {
    const installed = await installManagedThriftls(
      {
        manifestURL,
        storageDir,
        platform: { os: 'linux', arch: 'amd64' },
      },
      {
        fetchBytes: fetchFromMap,
      },
    );

    const content = await fs.readFile(installed, 'utf8');
    assert.match(content, /echo ok/);
  } finally {
    await fs.rm(storageDir, { recursive: true, force: true });
  }
});

test('installManagedThriftls refuses checksum mismatch', async () => {
  const storageDir = await fs.mkdtemp(path.join(os.tmpdir(), 'thrift-weaver-test-install-mismatch-'));
  const archive = await createTarGzBinaryArchive('thriftls', '#!/bin/sh\necho mismatch\n');

  const manifestURL = 'https://example.invalid/manifest.json';
  const archiveURL = 'https://example.invalid/thriftls_1.0.0_linux_amd64.tar.gz';
  const manifest = manifestJSON('1.0.0', {
    os: 'linux',
    arch: 'amd64',
    url: archiveURL,
    sha: '0'.repeat(64),
    size: archive.byteLength,
    filename: 'thriftls_1.0.0_linux_amd64.tar.gz',
  });

  const data = new Map<string, Uint8Array>([
    [manifestURL, Buffer.from(manifest, 'utf8')],
    [archiveURL, archive],
  ]);
  const fetchFromMap = async (url: string): Promise<Uint8Array> => {
    const value = data.get(url);
    assert.ok(value, `unexpected URL ${url}`);
    return value;
  };

  try {
    await assert.rejects(
      installManagedThriftls(
        {
          manifestURL,
          storageDir,
          platform: { os: 'linux', arch: 'amd64' },
        },
        {
          fetchBytes: fetchFromMap,
        },
      ),
      /checksum mismatch/,
    );
  } finally {
    await fs.rm(storageDir, { recursive: true, force: true });
  }
});

test('installManagedThriftls rolls back to last-known-good on failed update', async () => {
  const storageDir = await fs.mkdtemp(path.join(os.tmpdir(), 'thrift-weaver-test-install-rollback-'));
  const archive = await createTarGzBinaryArchive('thriftls', '#!/bin/sh\necho new\n');
  const archiveSHA = sha256Hex(archive);

  const currentDir = path.join(storageDir, 'managed-thriftls', 'current');
  const currentBin = path.join(currentDir, 'thriftls');
  await fs.mkdir(currentDir, { recursive: true });
  await fs.writeFile(currentBin, '#!/bin/sh\necho old\n', { mode: 0o755 });

  const manifestURL = 'https://example.invalid/manifest.json';
  const archiveURL = 'https://example.invalid/thriftls_1.0.1_linux_amd64.tar.gz';
  const manifest = manifestJSON('1.0.1', {
    os: 'linux',
    arch: 'amd64',
    url: archiveURL,
    sha: archiveSHA,
    size: archive.byteLength,
    filename: 'thriftls_1.0.1_linux_amd64.tar.gz',
  });

  const data = new Map<string, Uint8Array>([
    [manifestURL, Buffer.from(manifest, 'utf8')],
    [archiveURL, archive],
  ]);
  const fetchFromMap = async (url: string): Promise<Uint8Array> => {
    const value = data.get(url);
    assert.ok(value, `unexpected URL ${url}`);
    return value;
  };
  const warnings: string[] = [];

  try {
    const installed = await installManagedThriftls(
      {
        manifestURL,
        storageDir,
        platform: { os: 'linux', arch: 'amd64' },
        failAfterBackupForTest: true,
      },
      {
        fetchBytes: fetchFromMap,
        warn: (message) => warnings.push(message),
      },
    );

    assert.equal(installed, currentBin);
    const content = await fs.readFile(currentBin, 'utf8');
    assert.match(content, /echo old/);
    assert.equal(warnings.length, 1);
    assert.match(warnings[0], /simulated failure/);
  } finally {
    await fs.rm(storageDir, { recursive: true, force: true });
  }
});

type Release = {
  version: string;
  binaryContents: string;
  // Overrides the manifest checksum to simulate a tampered or corrupt archive.
  manifestSHA?: string;
};

// Serves one release at a time from a fake manifest endpoint and records
// every URL fetched, so tests can tell a manifest check from a download.
async function fakeReleaseServer(initial: Release) {
  const manifestURL = 'https://example.invalid/manifest.json';
  const fetched: string[] = [];
  let offline = false;
  let data = new Map<string, Uint8Array>();

  async function publish(release: Release): Promise<void> {
    const archive = await createTarGzBinaryArchive('thriftls', release.binaryContents);
    const filename = `thriftls_${release.version}_linux_amd64.tar.gz`;
    const archiveURL = `https://example.invalid/${filename}`;
    const manifest = manifestJSON(release.version, {
      os: 'linux',
      arch: 'amd64',
      url: archiveURL,
      sha: release.manifestSHA ?? sha256Hex(archive),
      size: archive.byteLength,
      filename,
    });
    data = new Map([
      [manifestURL, Buffer.from(manifest, 'utf8')],
      [archiveURL, archive],
    ]);
  }
  await publish(initial);

  return {
    manifestURL,
    fetched,
    publish,
    setOffline(value: boolean): void {
      offline = value;
    },
    async fetchBytes(url: string): Promise<Uint8Array> {
      fetched.push(url);
      if (offline) {
        throw new Error('getaddrinfo ENOTFOUND example.invalid');
      }
      const value = data.get(url);
      assert.ok(value, `unexpected URL ${url}`);
      return value;
    },
  };
}

async function withStorage(fn: (storageDir: string) => Promise<void>): Promise<void> {
  const storageDir = await fs.mkdtemp(path.join(os.tmpdir(), 'thrift-weaver-test-install-reuse-'));
  try {
    await fn(storageDir);
  } finally {
    await fs.rm(storageDir, { recursive: true, force: true });
  }
}

test('installManagedThriftls reuses the installed version and downloads only a new one', async () => {
  await withStorage(async (storageDir) => {
    const server = await fakeReleaseServer({ version: '1.0.0', binaryContents: '#!/bin/sh\necho v1\n' });
    const install = () =>
      installManagedThriftls(
        { manifestURL: server.manifestURL, storageDir, platform: { os: 'linux', arch: 'amd64' } },
        { fetchBytes: (url) => server.fetchBytes(url) },
      );

    const first = await install();
    assert.equal(server.fetched.length, 2);

    server.fetched.length = 0;
    const second = await install();
    assert.equal(second, first);
    assert.deepEqual(server.fetched, [server.manifestURL]);

    server.fetched.length = 0;
    await server.publish({ version: '1.0.1', binaryContents: '#!/bin/sh\necho v2\n' });
    const third = await install();
    assert.equal(third, first);
    assert.equal(server.fetched.length, 2);
    assert.match(await fs.readFile(third, 'utf8'), /echo v2/);

    const managedEntries = await fs.readdir(path.join(storageDir, 'managed-thriftls'));
    assert.deepEqual(managedEntries, ['current']);
  });
});

test('installManagedThriftls falls back to the installed binary when the update cannot be fetched', async () => {
  await withStorage(async (storageDir) => {
    const server = await fakeReleaseServer({ version: '1.0.0', binaryContents: '#!/bin/sh\necho v1\n' });
    const warnings: string[] = [];
    const install = () =>
      installManagedThriftls(
        { manifestURL: server.manifestURL, storageDir, platform: { os: 'linux', arch: 'amd64' } },
        { fetchBytes: (url) => server.fetchBytes(url), warn: (message) => warnings.push(message) },
      );

    server.setOffline(true);
    await assert.rejects(install(), /ENOTFOUND/);
    assert.deepEqual(warnings, []);

    server.setOffline(false);
    const installed = await install();

    server.setOffline(true);
    assert.equal(await install(), installed);
    assert.match(await fs.readFile(installed, 'utf8'), /echo v1/);
    assert.equal(warnings.length, 1);
    assert.match(warnings[0], /ENOTFOUND/);
  });
});

test('installManagedThriftls keeps the installed binary when a new archive fails its checksum', async () => {
  await withStorage(async (storageDir) => {
    const server = await fakeReleaseServer({ version: '1.0.0', binaryContents: '#!/bin/sh\necho v1\n' });
    const warnings: string[] = [];
    const install = () =>
      installManagedThriftls(
        { manifestURL: server.manifestURL, storageDir, platform: { os: 'linux', arch: 'amd64' } },
        { fetchBytes: (url) => server.fetchBytes(url), warn: (message) => warnings.push(message) },
      );

    const installed = await install();
    await server.publish({ version: '1.0.1', binaryContents: '#!/bin/sh\necho evil\n', manifestSHA: '0'.repeat(64) });

    assert.equal(await install(), installed);
    assert.match(await fs.readFile(installed, 'utf8'), /echo v1/);
    assert.equal(warnings.length, 1);
    assert.match(warnings[0], /checksum mismatch/);

    // The rejected release must not be recorded as installed: the next start
    // tries the download again instead of trusting the old binary as 1.0.1.
    server.fetched.length = 0;
    await install();
    assert.equal(server.fetched.length, 2);
  });
});

test('assertSafeArchiveEntryPath rejects traversal and absolute paths', () => {
  for (const unsafePath of ['../evil', '/abs/path', 'C:/windows/path', '..\\evil']) {
    assert.throws(() => assertSafeArchiveEntryPath(unsafePath), /unsafe archive entry path/);
  }
  assert.doesNotThrow(() => assertSafeArchiveEntryPath('bin/thriftls'));
  assert.doesNotThrow(() => assertSafeArchiveEntryPath('thriftls'));
});
