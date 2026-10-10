const vscode = require('vscode');
const assert = require('assert');
exports.run = async function() {
 console.log('Findrail VS Code extension-host assertions starting');
 const http=require('node:http');const request=http.request;let requests=0;http.request=function(...args){requests++;return request.apply(this,args)};
 const ext=vscode.extensions.getExtension('findrail-local.findrail-readonly');
 assert.ok(ext,'extension is installed in the test host');
 await ext.activate();
 const commands=await vscode.commands.getCommands(true);
 for(const name of ['connect','chooseSource','search','searchSelection','setMode','setFilters','refreshSources','disconnect','nextPage','previousPage','refreshEvidence','copyLocation','copyCitation']) assert.ok(commands.includes(`findrail.${name}`),`registered ${name}`);
 assert.strictEqual(requests,0,'activation does not initiate HTTP requests');http.request=request;
 const virtual=await vscode.workspace.openTextDocument(vscode.Uri.from({scheme:'findrail-evidence',path:'/not-a-session-key'}));assert.match(virtual.getText(),/no longer available/,'arbitrary virtual URI cannot retrieve server evidence');assert.strictEqual(virtual.isDirty,false);
 console.log('Findrail VS Code extension-host assertions passed');
};
