const vscode = require("vscode");
const { LanguageClient, Trace, TransportKind } = require("vscode-languageclient/node");

let client;

async function activate(context) {
  const outputChannel = vscode.window.createOutputChannel("Osty Language Server");

  async function restart() {
    if (client) {
      await client.stop();
      client = undefined;
    }
    client = createClient(outputChannel);
    await client.start();
  }

  context.subscriptions.push(
    vscode.commands.registerCommand("osty.restartLanguageServer", restart),
    outputChannel,
    {
      dispose: () => {
        if (client) {
          void client.stop();
        }
      }
    }
  );

  await restart();
}

function createClient(outputChannel) {
  const config = vscode.workspace.getConfiguration("osty.languageServer");
  const command = config.get("command", "osty");
  const configuredArgs = config.get("args", ["lsp"]);
  const args = Array.isArray(configuredArgs) ? configuredArgs : ["lsp"];
  const trace = config.get("trace.server", "off");
  const workspaceFolder = vscode.workspace.workspaceFolders?.[0];

  const serverOptions = {
    command,
    args,
    transport: TransportKind.stdio,
    options: {
      cwd: workspaceFolder?.uri.fsPath
    }
  };

  const clientOptions = {
    documentSelector: [
      { scheme: "file", language: "osty" },
      { scheme: "untitled", language: "osty" }
    ],
    outputChannel,
    traceOutputChannel: outputChannel,
    synchronize: {
      configurationSection: "osty",
      fileEvents: vscode.workspace.createFileSystemWatcher("**/*.{osty,toml}")
    }
  };

  const languageClient = new LanguageClient(
    "ostyLanguageServer",
    "Osty Language Server",
    serverOptions,
    clientOptions
  );
  languageClient.setTrace(traceLevel(trace));
  return languageClient;
}

function traceLevel(value) {
  switch (value) {
    case "messages":
      return Trace.Messages;
    case "verbose":
      return Trace.Verbose;
    default:
      return Trace.Off;
  }
}

function deactivate() {
  if (!client) {
    return undefined;
  }
  return client.stop();
}

module.exports = {
  activate,
  deactivate
};
