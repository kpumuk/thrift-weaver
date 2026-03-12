export function buildServerArgs(serverArgs: string[], workspaceIndexWorkers: number): string[] {
  const args = [...serverArgs];
  if (workspaceIndexWorkers > 0) {
    args.push(`--workspace-index-workers=${Math.trunc(workspaceIndexWorkers)}`);
  }
  return args;
}
