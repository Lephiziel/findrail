import test from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import fs from 'node:fs';
import { createRequire } from 'node:module';

const require = createRequire(import.meta.url);
const Module = require('node:module');

test('extension does not persist queries, inventory, results, or evidence', () => {
 const implementation=fs.readFileSync(new URL('../src/extension.ts',import.meta.url),'utf8');
 assert.doesNotMatch(implementation,/globalState|workspaceState/);
});

function loadExtension(endpoint) {
 const commands = new Map();
 let provider, clipboard = '';
 const disposable = () => ({ dispose() {} });
 const vscode = {
  EventEmitter: class { event = () => disposable(); dispose() {} },
  Uri: { from: value => ({ ...value }) },
  workspace: {
   getConfiguration: () => ({ inspect: key => ({ globalValue: key === 'endpoint' ? endpoint : undefined }) }),
   registerTextDocumentContentProvider: (_scheme, value) => { provider = value; return disposable() },
   onDidChangeConfiguration: disposable,
   onDidCloseTextDocument: disposable,
   openTextDocument: async uri => ({ uri, getText: () => provider.provideTextDocumentContent(uri) })
  },
  window: {
   showErrorMessage() {}, showInformationMessage() {}, showWarningMessage() {},
   showQuickPick: async items => items[0],
   showInputBox: async () => 'term',
   showTextDocument: async document => { vscode.window.activeTextEditor = { document }; },
   onDidChangeActiveTextEditor: disposable
  },
  commands: {
   registerCommand: (name, fn) => { commands.set(name, fn); return disposable() },
   executeCommand: async (name, ...args) => commands.get(name)?.(...args)
  },
  env: { clipboard: { writeText: async value => { clipboard = value } } }
 };
 const filename = require.resolve('../dist/extension.js');
 const original = Module._load;
 delete require.cache[filename];
 Module._load = function(name, ...args) { return name === 'vscode' ? vscode : original.call(this, name, ...args) };
 let extension;
 try { extension = require(filename) } finally { Module._load = original }
 return { extension, vscode, clipboard: () => clipboard };
}

test('failed capability negotiation is retried before any inventory request', async () => {
 const requested = [];
 const server = http.createServer((req, res) => {
  requested.push(req.url); res.setHeader('content-type', 'application/json');
  res.end(JSON.stringify(req.url === '/healthz' ? { status: 'ok' } : req.url === '/api/v1/sources' ? { sources: [] } : {}));
 });
 await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
 const { extension, vscode } = loadExtension(`http://127.0.0.1:${server.address().port}`);
 try {
  await extension.activate({ subscriptions: [] });
  await vscode.commands.executeCommand('findrail.chooseSource');
  await vscode.commands.executeCommand('findrail.chooseSource');
  assert.deepEqual(requested, ['/healthz', '/api/v1/capabilities', '/healthz', '/api/v1/capabilities']);
 } finally { extension.deactivate(); await new Promise(resolve => server.close(resolve)) }
});

test('preview and copied citations use the fetched evidence commit rather than the search commit', async () => {
 const source = { id: 'source_test', name: 'Synthetic', kind: 'github', root: 'https://github.com/Example/Demo', documents: 1, last_indexed_at: '2026-10-10T00:00:00Z' };
 const uri = sha => `https://github.com/Example/Demo/blob/${sha.repeat(40)}/note.md`;
 const result = { id: 'doc_test', title: 'note.md', path: 'note.md', uri: uri('a'), source_id: source.id, source_name: source.name, source_kind: source.kind, media_type: 'text/plain', snippet: 'old term', score: 1 };
 const evidence = { ...result, uri: uri('b'), text: 'new term', content_hash: 'new-hash', modified_at: '2026-10-11T00:00:00Z', truncated: false };
 const server = http.createServer((req, res) => {
  res.setHeader('content-type', 'application/json');
  const body = req.url === '/healthz' ? { status: 'ok' } : req.url === '/api/v1/capabilities' ? { client_api_version: 1, search_modes: ['literal', 'advanced'], search_filters: ['source', 'format', 'path_prefix', 'title_contains'], pdf_title_only_evidence: true } : req.url === '/api/v1/sources' ? { sources: [source] } : req.url.startsWith('/api/v1/search?') ? { query: 'term', total: 1, results: [result] } : evidence;
  res.end(JSON.stringify(body));
 });
 await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
 const { extension, vscode, clipboard } = loadExtension(`http://127.0.0.1:${server.address().port}`);
 try {
  await extension.activate({ subscriptions: [] });
  await vscode.commands.executeCommand('findrail.search');
  const preview = vscode.window.activeTextEditor.document.getText();
  assert.ok(preview.includes(uri('b'))); assert.ok(preview.includes('new term'));
  await vscode.commands.executeCommand('findrail.copyLocation'); assert.equal(clipboard(), uri('b'));
  await vscode.commands.executeCommand('findrail.copyCitation'); assert.ok(clipboard().includes(uri('b')));
 } finally { extension.deactivate(); await new Promise(resolve => server.close(resolve)) }
});
