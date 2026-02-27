import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const extensionRoot = path.resolve(__dirname, '..');
const repoRoot = path.resolve(extensionRoot, '..', '..');

class LspPeer {
  constructor(proc) {
    this.proc = proc;
    this.nextID = 1;
    this.pending = new Map();
    this.notifications = [];
    this.waiting = [];
    this.buffer = Buffer.alloc(0);

    proc.stdout.on('data', (chunk) => {
      this.onData(chunk);
    });
  }

  onData(chunk) {
    this.buffer = Buffer.concat([this.buffer, chunk]);
    while (true) {
      const headerEnd = this.buffer.indexOf('\r\n\r\n');
      if (headerEnd < 0) {
        return;
      }
      const headerText = this.buffer.slice(0, headerEnd).toString('utf8');
      const match = /Content-Length:\s*(\d+)/i.exec(headerText);
      if (!match) {
        throw new Error(`missing Content-Length header: ${headerText}`);
      }
      const len = Number(match[1]);
      const msgStart = headerEnd + 4;
      if (this.buffer.length < msgStart + len) {
        return;
      }
      const jsonBytes = this.buffer.slice(msgStart, msgStart + len);
      this.buffer = this.buffer.slice(msgStart + len);
      const msg = JSON.parse(jsonBytes.toString('utf8'));
      this.handleMessage(msg);
    }
  }

  handleMessage(msg) {
    if (Object.prototype.hasOwnProperty.call(msg, 'id')) {
      const resolver = this.pending.get(msg.id);
      if (resolver) {
        this.pending.delete(msg.id);
        resolver(msg);
      }
      return;
    }

    this.notifications.push(msg);
    this.waiting = this.waiting.filter((w) => {
      if (w.method !== msg.method) {
        return true;
      }
      w.resolve(msg);
      return false;
    });
  }

  write(payload) {
    const json = Buffer.from(JSON.stringify(payload), 'utf8');
    const header = Buffer.from(`Content-Length: ${json.length}\r\n\r\n`, 'utf8');
    this.proc.stdin.write(header);
    this.proc.stdin.write(json);
  }

  request(method, params) {
    const id = this.nextID++;
    const msg = {
      jsonrpc: '2.0',
      id,
      method,
      params,
    };
    this.write(msg);
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(id);
        reject(new Error(`request timeout for ${method}`));
      }, 10000);
      this.pending.set(id, (response) => {
        clearTimeout(timer);
        resolve(response);
      });
    });
  }

  notify(method, params) {
    this.write({
      jsonrpc: '2.0',
      method,
      params,
    });
  }

  waitForNotification(method, timeoutMs = 10000) {
    const existing = this.notifications.find((msg) => msg.method === method);
    if (existing) {
      return Promise.resolve(existing);
    }
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        this.waiting = this.waiting.filter((w) => w.resolve !== resolve);
        reject(new Error(`notification timeout for ${method}`));
      }, timeoutMs);
      this.waiting.push({
        method,
        resolve: (msg) => {
          clearTimeout(timer);
          resolve(msg);
        },
      });
    });
  }
}

async function main() {
  const proc = spawn('go', ['run', './cmd/thriftls'], {
    cwd: repoRoot,
    stdio: ['pipe', 'pipe', 'pipe'],
    env: process.env,
  });

  let stderr = '';
  proc.stderr.on('data', (chunk) => {
    stderr += chunk.toString('utf8');
  });

  const peer = new LspPeer(proc);
  const uri = 'file:///smoke.thrift';

  try {
    const init = await peer.request('initialize', {
      processId: process.pid,
      rootUri: null,
      capabilities: {},
      clientInfo: { name: 'thrift-weaver-smoke', version: '0.0.0' },
    });
    assert.equal(init.error, undefined, `initialize error: ${JSON.stringify(init.error)}`);
    assert.equal(init.result?.capabilities?.documentFormattingProvider, true);

    peer.notify('initialized', {});

    peer.notify('textDocument/didOpen', {
      textDocument: {
        uri,
        languageId: 'thrift',
        version: 1,
        text: 'struct Broken {\n',
      },
    });

    const diag = await peer.waitForNotification('textDocument/publishDiagnostics');
    assert.equal(diag.params.uri, uri);
    assert.ok(Array.isArray(diag.params.diagnostics));
    assert.ok(diag.params.diagnostics.length > 0, 'expected parse diagnostics for invalid input');

    peer.notify('textDocument/didChange', {
      textDocument: {
        uri,
        version: 2,
      },
      contentChanges: [
        {
          text: 'struct S {\n  1: string a\n}\n',
        },
      ],
    });

    const formatResponse = await peer.request('textDocument/formatting', {
      textDocument: { uri },
      options: {
        tabSize: 2,
        insertSpaces: true,
      },
    });
    assert.equal(formatResponse.error, undefined, `formatting error: ${JSON.stringify(formatResponse.error)}`);
    assert.ok(Array.isArray(formatResponse.result), 'expected formatting result array');

    await peer.request('shutdown', null);
    peer.notify('exit', null);

    process.stdout.write('lsp smoke checks passed\n');
  } finally {
    proc.kill();
    if (stderr.trim() !== '') {
      process.stderr.write(stderr);
    }
  }
}

main().catch((err) => {
  process.stderr.write(`${err instanceof Error ? err.stack ?? err.message : String(err)}\n`);
  process.exitCode = 1;
});
