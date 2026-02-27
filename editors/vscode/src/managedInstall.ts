import * as crypto from 'node:crypto';
import * as fs from 'node:fs/promises';
import * as os from 'node:os';
import * as path from 'node:path';

import AdmZip = require('adm-zip');
import * as tar from 'tar';

export type ManagedInstallPlatform = {
  os: string;
  arch: string;
};

export type ManagedInstallConfig = {
  manifestURL: string;
  storageDir: string;
  platform?: ManagedInstallPlatform;
  allowInsecureHTTP?: boolean;
  failAfterBackupForTest?: boolean;
};

export type ManagedInstallDeps = {
  fetchBytes?: (url: string) => Promise<Uint8Array>;
  log?: (message: string) => void;
};

type RawManifest = {
  schema_version: number;
  tool: string;
  version: string;
  platforms: RawPlatformItem[];
};

type RawPlatformItem = {
  os: string;
  arch: string;
  url: string;
  sha256: string;
  size_bytes: number;
  filename: string;
};

export type Manifest = {
  schemaVersion: number;
  tool: string;
  version: string;
  platforms: ManifestPlatformItem[];
};

export type ManifestPlatformItem = {
  os: string;
  arch: string;
  url: string;
  sha256: string;
  sizeBytes: number;
  filename: string;
};

const managedRootDir = 'managed-thriftls';

export function parseManifestJSON(jsonText: string): Manifest {
  let parsed: unknown;
  try {
    parsed = JSON.parse(jsonText);
  } catch (err) {
    throw new Error(`invalid managed-install manifest JSON: ${String(err)}`);
  }

  const manifest = parsed as Partial<RawManifest>;
  if (manifest.schema_version !== 1) {
    throw new Error(`unsupported manifest schema_version: ${String(manifest.schema_version)}`);
  }
  if (manifest.tool !== 'thriftls') {
    throw new Error(`unsupported manifest tool: ${String(manifest.tool)}`);
  }
  if (typeof manifest.version !== 'string' || manifest.version.trim() === '') {
    throw new Error('manifest version is required');
  }
  if (!Array.isArray(manifest.platforms) || manifest.platforms.length === 0) {
    throw new Error('manifest platforms must be a non-empty array');
  }

  const platforms = manifest.platforms.map(validatePlatformItem);
  return {
    schemaVersion: manifest.schema_version,
    tool: manifest.tool,
    version: manifest.version,
    platforms,
  };
}

function validatePlatformItem(item: unknown): ManifestPlatformItem {
  const raw = item as Partial<RawPlatformItem>;
  if (typeof raw.os !== 'string' || raw.os.trim() === '') {
    throw new Error('manifest platform item is missing os');
  }
  if (typeof raw.arch !== 'string' || raw.arch.trim() === '') {
    throw new Error('manifest platform item is missing arch');
  }
  if (typeof raw.url !== 'string' || raw.url.trim() === '') {
    throw new Error('manifest platform item is missing url');
  }
  if (typeof raw.filename !== 'string' || raw.filename.trim() === '') {
    throw new Error('manifest platform item is missing filename');
  }
  if (typeof raw.sha256 !== 'string' || !/^[a-fA-F0-9]{64}$/.test(raw.sha256)) {
    throw new Error('manifest platform item has invalid sha256');
  }
  if (typeof raw.size_bytes !== 'number' || !Number.isFinite(raw.size_bytes) || raw.size_bytes <= 0) {
    throw new Error('manifest platform item has invalid size_bytes');
  }

  return {
    os: raw.os,
    arch: raw.arch,
    url: raw.url,
    sha256: raw.sha256.toLowerCase(),
    sizeBytes: Math.trunc(raw.size_bytes),
    filename: raw.filename,
  };
}

export function currentPlatform(): ManagedInstallPlatform {
  return {
    os: mapNodeOS(process.platform),
    arch: mapNodeArch(process.arch),
  };
}

function mapNodeOS(platform: NodeJS.Platform): string {
  switch (platform) {
    case 'linux':
      return 'linux';
    case 'darwin':
      return 'darwin';
    case 'win32':
      return 'windows';
    default:
      return platform;
  }
}

function mapNodeArch(arch: string): string {
  switch (arch) {
    case 'x64':
      return 'amd64';
    case 'arm64':
      return 'arm64';
    default:
      return arch;
  }
}

export function selectPlatformArtifact(manifest: Manifest, platform: ManagedInstallPlatform): ManifestPlatformItem {
  const candidates = manifest.platforms
    .filter((p) => p.os === platform.os && p.arch === platform.arch)
    .sort((a, b) => {
      if (a.filename !== b.filename) {
        return a.filename.localeCompare(b.filename);
      }
      return a.url.localeCompare(b.url);
    });

  if (candidates.length === 0) {
    throw new Error(`no managed-install artifact for platform ${platform.os}/${platform.arch}`);
  }
  return candidates[0];
}

export async function installManagedThriftls(
  config: ManagedInstallConfig,
  deps: ManagedInstallDeps = {},
): Promise<string> {
  const log = deps.log ?? (() => undefined);
  const allowInsecureHTTP = config.allowInsecureHTTP === true;
  const fetchBytes = deps.fetchBytes ?? ((url: string) => fetchBytesDefault(url, allowInsecureHTTP));

  if (config.manifestURL.trim() === '') {
    throw new Error('managed install manifest URL is empty');
  }

  const platform = config.platform ?? currentPlatform();
  const manifestRaw = await fetchBytes(config.manifestURL);
  const manifest = parseManifestJSON(bytesToUTF8(manifestRaw));
  const artifact = selectPlatformArtifact(manifest, platform);

  log(`managed-install: selected ${artifact.filename} for ${platform.os}/${platform.arch}`);

  const archiveBytes = await fetchBytes(artifact.url);
  if (archiveBytes.byteLength !== artifact.sizeBytes) {
    throw new Error(
      `managed install artifact size mismatch: got ${archiveBytes.byteLength}, expected ${artifact.sizeBytes}`,
    );
  }

  const actualSHA = sha256Hex(archiveBytes);
  if (actualSHA !== artifact.sha256) {
    throw new Error(`managed install checksum mismatch for ${artifact.filename}`);
  }

  const tempRoot = await fs.mkdtemp(path.join(os.tmpdir(), 'thrift-weaver-install-'));
  const archivePath = path.join(tempRoot, artifact.filename);
  const extractDir = path.join(tempRoot, 'extract');
  await fs.mkdir(extractDir, { recursive: true });
  await fs.writeFile(archivePath, archiveBytes);

  const managedRoot = path.join(config.storageDir, managedRootDir);
  const binaryName = platform.os === 'windows' ? 'thriftls.exe' : 'thriftls';

  try {
    await extractArchiveSafe(archivePath, extractDir);
    const extractedBinary = await findBinary(extractDir, binaryName);
    if (extractedBinary === '') {
      throw new Error(`managed install archive does not contain ${binaryName}`);
    }

    const versionDirName = `${manifest.version}-${platform.os}-${platform.arch}`;
    const versionDir = path.join(managedRoot, 'versions', versionDirName);
    await fs.mkdir(versionDir, { recursive: true });
    const versionedBinary = path.join(versionDir, binaryName);
    await fs.copyFile(extractedBinary, versionedBinary);
    if (platform.os !== 'windows') {
      await fs.chmod(versionedBinary, 0o755);
    }

    const currentDir = path.join(managedRoot, 'current');
    const backupDir = path.join(managedRoot, 'backup');
    const stagingCurrentDir = path.join(managedRoot, `staging-${Date.now()}`);
    await fs.mkdir(stagingCurrentDir, { recursive: true });
    const stagingBinary = path.join(stagingCurrentDir, binaryName);
    await fs.copyFile(versionedBinary, stagingBinary);
    if (platform.os !== 'windows') {
      await fs.chmod(stagingBinary, 0o755);
    }

    await removeDirIfExists(backupDir);
    if (await pathExists(currentDir)) {
      await fs.rename(currentDir, backupDir);
    }

    try {
      if (config.failAfterBackupForTest) {
        throw new Error('managed install simulated failure after backup');
      }
      await fs.rename(stagingCurrentDir, currentDir);
      await removeDirIfExists(backupDir);
    } catch (err) {
      await removeDirIfExists(currentDir);
      if (await pathExists(backupDir)) {
        await fs.rename(backupDir, currentDir);
      }
      throw err;
    }

    const currentBinary = path.join(currentDir, binaryName);
    if (!(await pathExists(currentBinary))) {
      throw new Error('managed install completed without current binary');
    }
    return currentBinary;
  } finally {
    await removeDirIfExists(tempRoot);
  }
}

async function fetchBytesDefault(urlString: string, allowInsecureHTTP: boolean): Promise<Uint8Array> {
  const url = new URL(urlString);
  if (!allowInsecureHTTP && url.protocol !== 'https:') {
    throw new Error(`managed install refuses non-HTTPS URL: ${urlString}`);
  }

  const response = await fetch(url);
  if (!response.ok) {
    throw new Error(`managed install HTTP ${response.status} for ${urlString}`);
  }

  const buffer = new Uint8Array(await response.arrayBuffer());
  if (buffer.byteLength === 0) {
    throw new Error(`managed install download is empty: ${urlString}`);
  }
  return buffer;
}

function bytesToUTF8(data: Uint8Array): string {
  return Buffer.from(data).toString('utf8');
}

function sha256Hex(data: Uint8Array): string {
  return crypto.createHash('sha256').update(data).digest('hex').toLowerCase();
}

async function extractArchiveSafe(archivePath: string, extractDir: string): Promise<void> {
  if (archivePath.endsWith('.zip')) {
    await extractZipSafe(archivePath, extractDir);
    return;
  }
  if (archivePath.endsWith('.tar.gz')) {
    await extractTarGzSafe(archivePath, extractDir);
    return;
  }
  throw new Error(`unsupported managed install archive format: ${path.basename(archivePath)}`);
}

async function extractZipSafe(archivePath: string, extractDir: string): Promise<void> {
  const zip = new AdmZip(archivePath);
  const entries = zip.getEntries();
  for (const entry of entries) {
    assertSafeArchiveEntryPath(entry.entryName);
  }
  zip.extractAllTo(extractDir, true);
}

async function extractTarGzSafe(archivePath: string, extractDir: string): Promise<void> {
  await tar.t({
    file: archivePath,
    gzip: true,
    onReadEntry(entry: { path: string }): void {
      assertSafeArchiveEntryPath(entry.path);
    },
  });

  await tar.x({
    file: archivePath,
    cwd: extractDir,
    gzip: true,
    strict: true,
  });
}

export function assertSafeArchiveEntryPath(entryPath: string): void {
  const unix = entryPath.replaceAll('\\', '/');
  const normalized = path.posix.normalize(unix);
  if (normalized === '' || normalized === '.' || normalized.startsWith('/')) {
    throw new Error(`unsafe archive entry path: ${entryPath}`);
  }
  if (normalized === '..' || normalized.startsWith('../') || normalized.includes('/../')) {
    throw new Error(`unsafe archive entry path: ${entryPath}`);
  }
  if (/^[A-Za-z]:/.test(normalized)) {
    throw new Error(`unsafe archive entry path: ${entryPath}`);
  }
}

async function findBinary(root: string, binaryName: string): Promise<string> {
  const matches: string[] = [];

  async function walk(dir: string): Promise<void> {
    const entries = await fs.readdir(dir, { withFileTypes: true });
    for (const entry of entries) {
      const full = path.join(dir, entry.name);
      if (entry.isDirectory()) {
        await walk(full);
        continue;
      }
      if (entry.isFile() && entry.name === binaryName) {
        matches.push(full);
      }
    }
  }

  await walk(root);
  matches.sort((a, b) => {
    const depthA = a.split(path.sep).length;
    const depthB = b.split(path.sep).length;
    if (depthA !== depthB) {
      return depthA - depthB;
    }
    return a.localeCompare(b);
  });
  return matches[0] ?? '';
}

async function pathExists(targetPath: string): Promise<boolean> {
  try {
    await fs.stat(targetPath);
    return true;
  } catch {
    return false;
  }
}

async function removeDirIfExists(dirPath: string): Promise<void> {
  await fs.rm(dirPath, { recursive: true, force: true });
}
