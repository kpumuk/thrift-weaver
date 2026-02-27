import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import fs from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const extensionRoot = path.resolve(__dirname, '..');

async function runNodeScript(relPath) {
  const scriptPath = path.join(extensionRoot, relPath);
  await new Promise((resolve, reject) => {
    const proc = spawn(process.execPath, [scriptPath], {
      cwd: extensionRoot,
      stdio: 'inherit',
      env: process.env,
    });
    proc.on('exit', (code) => {
      if (code === 0) {
        resolve();
        return;
      }
      reject(new Error(`${relPath} failed with exit code ${String(code)}`));
    });
  });
}

async function main() {
  const packageJSONPath = path.join(extensionRoot, 'package.json');
  const packageJSON = JSON.parse(await fs.readFile(packageJSONPath, 'utf8'));

  assert.ok(packageJSON.activationEvents.includes('onLanguage:thrift'));
  assert.ok(packageJSON.activationEvents.includes('onCommand:thrift.restartLanguageServer'));
  assert.ok(
    Array.isArray(packageJSON.contributes?.languages) &&
      packageJSON.contributes.languages.some((lang) => lang.id === 'thrift'),
  );

  await runNodeScript(path.join('scripts', 'test-syntax.mjs'));
  await runNodeScript(path.join('scripts', 'test-lsp-smoke.mjs'));

  process.stdout.write('extension smoke checks passed\n');
}

main().catch((err) => {
  process.stderr.write(`${err instanceof Error ? err.stack ?? err.message : String(err)}\n`);
  process.exitCode = 1;
});
