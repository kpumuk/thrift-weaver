export type ServerPathSource = 'managed' | 'external' | 'none';

export type ResolveServerPathConfig = {
  externalPath: string;
  managedEnabled: boolean;
};

export type ResolveServerPathDeps = {
  externalPathExists: (path: string) => boolean;
  installManaged: () => Promise<string>;
  onManagedFailure?: (err: unknown) => void;
};

export type ResolveServerPathResult = {
  source: ServerPathSource;
  path: string;
  managedError?: unknown;
};

export async function resolveServerPath(
  config: ResolveServerPathConfig,
  deps: ResolveServerPathDeps,
): Promise<ResolveServerPathResult> {
  const externalPath = config.externalPath.trim();
  const hasExternal = externalPath !== '' && deps.externalPathExists(externalPath);

  if (!config.managedEnabled) {
    if (hasExternal) {
      return { source: 'external', path: externalPath };
    }
    return { source: 'none', path: '' };
  }

  try {
    const managedPath = (await deps.installManaged()).trim();
    if (managedPath === '') {
      throw new Error('managed install returned empty path');
    }
    return { source: 'managed', path: managedPath };
  } catch (err) {
    deps.onManagedFailure?.(err);
    if (hasExternal) {
      return { source: 'external', path: externalPath, managedError: err };
    }
    return { source: 'none', path: '', managedError: err };
  }
}
