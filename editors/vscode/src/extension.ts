import * as vscode from 'vscode';

let outputChannel: vscode.OutputChannel | undefined;

export function activate(context: vscode.ExtensionContext): void {
  outputChannel = vscode.window.createOutputChannel('Thrift Weaver');
  context.subscriptions.push(outputChannel);

  outputChannel.appendLine('Thrift Weaver extension activated');

  context.subscriptions.push(
    vscode.workspace.onDidChangeConfiguration((event) => {
      if (!event.affectsConfiguration('thrift')) {
        return;
      }
      outputChannel?.appendLine('Thrift configuration changed');
    }),
  );
}

export function deactivate(): void {
  outputChannel?.appendLine('Thrift Weaver extension deactivated');
  outputChannel?.dispose();
  outputChannel = undefined;
}
