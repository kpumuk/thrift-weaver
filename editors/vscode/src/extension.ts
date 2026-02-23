import * as fs from 'node:fs';
import * as vscode from 'vscode';
import {
  LanguageClient,
  LanguageClientOptions,
  ServerOptions,
  State,
  StateChangeEvent,
  Trace,
  TransportKind,
} from 'vscode-languageclient/node.js';

type TraceLevel = 'off' | 'messages' | 'verbose';

type ThriftConfig = {
  serverPath: string;
  serverArgs: string[];
  lineWidth: number;
  traceServer: TraceLevel;
};

let outputChannel: vscode.OutputChannel | undefined;
let traceChannel: vscode.OutputChannel | undefined;
let client: LanguageClient | undefined;
let lifecycleChain: Promise<void> = Promise.resolve();
let warnedMissingServerPath = false;

const thriftServerPathSettingsQuery = '@ext:kpumuk.thrift-weaver-vscode thrift.server.path';
const openSettingsAction = 'Open Settings';

export async function activate(context: vscode.ExtensionContext): Promise<void> {
  outputChannel = vscode.window.createOutputChannel('Thrift Weaver');
  traceChannel = vscode.window.createOutputChannel('Thrift Weaver LSP Trace');
  context.subscriptions.push(outputChannel, traceChannel);

  logInfo('extension activated');

  context.subscriptions.push(
    vscode.commands.registerCommand('thrift.restartLanguageServer', async () => {
      await runLifecycleTask(async () => {
        await restartLanguageClient(context, 'manual restart');
      });
    }),
  );

  context.subscriptions.push(
    vscode.workspace.onDidChangeConfiguration((event) => {
      if (!event.affectsConfiguration('thrift')) {
        return;
      }
      void runLifecycleTask(async () => {
        logInfo('thrift configuration changed');
        await restartLanguageClient(context, 'configuration change');
      });
    }),
  );

  await runLifecycleTask(async () => {
    await startLanguageClient(context, 'activation');
  });
}

export async function deactivate(): Promise<void> {
  await runLifecycleTask(async () => {
    await stopLanguageClient('deactivation');
  });
  logInfo('extension deactivated');
  traceChannel?.dispose();
  outputChannel?.dispose();
  traceChannel = undefined;
  outputChannel = undefined;
}

async function runLifecycleTask(task: () => Promise<void>): Promise<void> {
  lifecycleChain = lifecycleChain.then(task, task);
  try {
    await lifecycleChain;
  } finally {
    lifecycleChain = lifecycleChain.catch(() => undefined);
  }
}

async function restartLanguageClient(context: vscode.ExtensionContext, reason: string): Promise<void> {
  await stopLanguageClient(`restart: ${reason}`);
  await startLanguageClient(context, reason);
}

async function startLanguageClient(context: vscode.ExtensionContext, reason: string): Promise<void> {
  const config = readThriftConfig();
  if (config.serverPath === '') {
    await notifyMissingServerPath();
    logInfo(`language server not started (${reason}): thrift.server.path is empty`);
    return;
  }
  if (!fs.existsSync(config.serverPath)) {
    await notifyServerStartFailure(`Configured thrift.server.path does not exist: ${config.serverPath}`);
    logError(`language server not started (${reason}): path not found: ${config.serverPath}`);
    return;
  }

  const serverOptions: ServerOptions = {
    command: config.serverPath,
    args: config.serverArgs,
    transport: TransportKind.stdio,
  };
  const clientOptions: LanguageClientOptions = {
    documentSelector: [{ scheme: 'file', language: 'thrift' }],
    outputChannel,
    traceOutputChannel: traceChannel,
    synchronize: {
      configurationSection: 'thrift',
      fileEvents: vscode.workspace.createFileSystemWatcher('**/*.thrift'),
    },
    initializationOptions: {
      thrift: {
        format: {
          lineWidth: config.lineWidth,
        },
      },
    },
  };

  const nextClient = new LanguageClient('thriftls', 'Thrift Language Server', serverOptions, clientOptions);
  nextClient.onDidChangeState((event: StateChangeEvent) => {
    logInfo(`language client state: ${clientStateName(event.oldState)} -> ${clientStateName(event.newState)}`);
  });

  try {
    logInfo(
      `starting language server (${reason}): ${config.serverPath}${
        config.serverArgs.length > 0 ? ` ${config.serverArgs.join(' ')}` : ''
      }`,
    );
    context.subscriptions.push(nextClient);
    await nextClient.start();
    await nextClient.setTrace(traceLevel(config.traceServer));
    client = nextClient;
    warnedMissingServerPath = false;
    logInfo('language server started');
  } catch (err) {
    logError(`language server start failed: ${formatError(err)}`);
    await notifyServerStartFailure(`Failed to start thrift language server: ${formatError(err)}`);
    try {
      await nextClient.stop();
    } catch {
      // Best effort cleanup after failed startup.
    }
  }
}

async function stopLanguageClient(reason: string): Promise<void> {
  if (!client) {
    return;
  }
  const current = client;
  client = undefined;
  try {
    logInfo(`stopping language server (${reason})`);
    await current.stop();
    logInfo('language server stopped');
  } catch (err) {
    logError(`language server stop failed: ${formatError(err)}`);
  }
}

function readThriftConfig(): ThriftConfig {
  const cfg = vscode.workspace.getConfiguration('thrift');
  const pathValue = cfg.get<string>('server.path', '').trim();
  const argsValue = cfg.get<unknown[]>('server.args', []);
  const lineWidthValue = cfg.get<number>('format.lineWidth', 100);
  const traceValue = cfg.get<string>('trace.server', 'off');

  const serverArgs = Array.isArray(argsValue)
    ? argsValue.filter((v): v is string => typeof v === 'string')
    : [];
  const lineWidth = Number.isFinite(lineWidthValue) && lineWidthValue > 0 ? Math.trunc(lineWidthValue) : 100;
  const traceServer = traceValue === 'messages' || traceValue === 'verbose' ? traceValue : 'off';

  return {
    serverPath: pathValue,
    serverArgs,
    lineWidth,
    traceServer,
  };
}

async function notifyMissingServerPath(): Promise<void> {
  if (warnedMissingServerPath) {
    return;
  }
  warnedMissingServerPath = true;
  const action = await vscode.window.showWarningMessage(
    'Thrift Weaver: set `thrift.server.path` to a thriftls binary to enable diagnostics and formatting. Managed install will be added in a later milestone.',
    openSettingsAction,
  );
  await maybeOpenThriftServerPathSettings(action);
}

async function notifyServerStartFailure(message: string): Promise<void> {
  const action = await vscode.window.showErrorMessage(message, openSettingsAction);
  await maybeOpenThriftServerPathSettings(action);
}

async function maybeOpenThriftServerPathSettings(action: string | undefined): Promise<void> {
  if (action !== openSettingsAction) {
    return;
  }
  await vscode.commands.executeCommand('workbench.action.openSettings', thriftServerPathSettingsQuery);
}

function traceLevel(value: TraceLevel): Trace {
  switch (value) {
    case 'messages':
      return Trace.Messages;
    case 'verbose':
      return Trace.Verbose;
    default:
      return Trace.Off;
  }
}

function clientStateName(state: State): string {
  switch (state) {
    case State.Starting:
      return 'starting';
    case State.Running:
      return 'running';
    case State.Stopped:
      return 'stopped';
    default:
      return `unknown(${String(state)})`;
  }
}

function formatError(err: unknown): string {
  if (err instanceof Error) {
    return err.message;
  }
  return String(err);
}

function logInfo(message: string): void {
  outputChannel?.appendLine(`[info] ${message}`);
}

function logError(message: string): void {
  outputChannel?.appendLine(`[error] ${message}`);
}
