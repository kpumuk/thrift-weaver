import fs from 'node:fs/promises';
import path from 'node:path';
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';

import textmate from 'vscode-textmate';
import oniguruma from 'vscode-oniguruma';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const extensionRoot = path.resolve(__dirname, '..');
const require = createRequire(import.meta.url);
const { Registry } = textmate;
const { loadWASM, OnigScanner, OnigString } = oniguruma;

async function main() {
  const [grammar, source, expected] = await Promise.all([
    readJSONFixture('syntaxes', 'thrift.tmLanguage.json'),
    readTextFixture('test', 'syntax', 'fixtures', 'core.thrift'),
    readJSONFixture('test', 'syntax', 'fixtures', 'expected-scopes.json'),
  ]);

  await loadOniguruma();

  if (!Array.isArray(expected)) {
    throw new Error('expected-scopes.json must contain an array');
  }

  const registry = new Registry({
    onigLib: Promise.resolve({
      createOnigScanner(patterns) {
        return new OnigScanner(patterns);
      },
      createOnigString(value) {
        return new OnigString(value);
      },
    }),
    loadGrammar: async (scopeName) => (scopeName === 'source.thrift' ? grammar : null),
  });

  const tmGrammar = await registry.loadGrammar('source.thrift');
  if (!tmGrammar) {
    throw new Error('failed to load source.thrift grammar');
  }

  const tokenRows = tokenizeFixture(tmGrammar, source);
  for (const check of expected) {
    assertScopeCheck(tokenRows, check);
  }

  process.stdout.write(`syntax smoke checks passed (${expected.length} assertions)\n`);
}

async function loadOniguruma() {
  const wasmPath = require.resolve('vscode-oniguruma/release/onig.wasm');
  const wasm = await fs.readFile(wasmPath);
  await loadWASM(new Uint8Array(wasm));
}

function tokenizeFixture(grammar, source) {
  const rows = [];
  let ruleStack = null;
  for (const line of source.split(/\r?\n/)) {
    const res = grammar.tokenizeLine(line, ruleStack);
    ruleStack = res.ruleStack;
    const tokens = res.tokens.map((tok) => ({
      text: line.slice(tok.startIndex, tok.endIndex),
      scopes: tok.scopes,
    }));
    rows.push(tokens);
  }
  return rows;
}

async function readTextFixture(...parts) {
  const filePath = path.join(extensionRoot, ...parts);
  return fs.readFile(filePath, 'utf8');
}

async function readJSONFixture(...parts) {
  return JSON.parse(await readTextFixture(...parts));
}

function assertScopeCheck(rows, check) {
  const line = Number(check.line);
  if (!Number.isInteger(line) || line < 1 || line > rows.length) {
    throw new Error(`invalid check line: ${JSON.stringify(check)}`);
  }
  const expectedText = typeof check.text === 'string' ? check.text : null;
  const expectedTextContains = typeof check.textContains === 'string' ? check.textContains : null;
  if ((expectedText === null || expectedText.length === 0) && (expectedTextContains === null || expectedTextContains.length === 0)) {
    throw new Error(`invalid check text/textContains: ${JSON.stringify(check)}`);
  }
  if (!Array.isArray(check.scopeIncludes) || check.scopeIncludes.length === 0) {
    throw new Error(`invalid check scopeIncludes: ${JSON.stringify(check)}`);
  }

  const tokens = rows[line - 1];
  const match = tokens.find((tok) => {
    if (expectedText !== null) {
      return tok.text === expectedText;
    }
    return tok.text.includes(expectedTextContains);
  });
  if (!match) {
    const tokenDesc = expectedText !== null ? JSON.stringify(expectedText) : `contains ${JSON.stringify(expectedTextContains)}`;
    throw new Error(`line ${line}: token ${tokenDesc} not found`);
  }
  for (const want of check.scopeIncludes) {
    if (!match.scopes.includes(want)) {
      throw new Error(
        `line ${line}: token ${JSON.stringify(match.text)} missing scope ${want}; got ${match.scopes.join(', ')}`,
      );
    }
  }
}

main().catch((err) => {
  process.stderr.write(`${err instanceof Error ? err.stack ?? err.message : String(err)}\n`);
  process.exitCode = 1;
});
