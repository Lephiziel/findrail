const vscode = require('vscode');
const assert = require('assert');
const fs = require('node:fs');
const path = require('node:path');
const { execFileSync } = require('node:child_process');
exports.run = async function() {
 console.log('Findrail VS Code extension-host assertions starting');
 if(process.env.FINDRAIL_TEST_RESTRICTED==='1')assert.strictEqual(vscode.workspace.isTrusted,false,'host runs the extension in Restricted Mode');
 const http=require('node:http');const request=http.request;let requests=0;http.request=function(...args){requests++;return request.apply(this,args)};
 const ext=vscode.extensions.getExtension('findrail-local.findrail-readonly');
 assert.ok(ext,'extension is installed in the test host');
 await ext.activate();
 const commands=await vscode.commands.getCommands(true);
 for(const name of ['connect','chooseSource','search','searchSelection','setMode','setFilters','refreshSources','disconnect','nextPage','previousPage','refreshEvidence','copyLocation','copyCitation']) assert.ok(commands.includes(`findrail.${name}`),`registered ${name}`);
 assert.strictEqual(requests,0,'activation does not initiate HTTP requests');http.request=request;
 const virtual=await vscode.workspace.openTextDocument(vscode.Uri.from({scheme:'findrail-evidence',path:'/not-a-session-key'}));assert.match(virtual.getText(),/no longer available/,'arbitrary virtual URI cannot retrieve server evidence');assert.strictEqual(virtual.isDirty,false);
 const inventory={id:'source_test',kind:'filesystem',name:'Synthetic notes',root:'/tmp/synthetic',documents:1,last_indexed_at:'2026-10-10T00:00:00Z'};
 const result={id:'doc_test',title:'runbook.pdf',uri:'file:///tmp/synthetic/runbook.pdf#page=1',path:'runbook.pdf',source_id:inventory.id,source_name:inventory.name,source_kind:inventory.kind,snippet:'webhook',score:-1,media_type:'application/pdf',page:1,page_count:2};
   let currentResult=result,overrideQuery='webhook',inventoryList=[inventory],blockPage2=false;const requestsSeen=[];let releaseSlowSearch,slowSearchStarted,releasePage2,page2Started;
   const slowSearchGate=new Promise(resolve=>{releaseSlowSearch=resolve});const slowSearchReady=new Promise(resolve=>{slowSearchStarted=resolve});const page2Gate=new Promise(resolve=>{releasePage2=resolve});const page2Ready=new Promise(resolve=>{page2Started=resolve});
   const server=http.createServer(async(req,res)=>{
    requestsSeen.push(`${req.method} ${req.url}`);res.setHeader('content-type','application/json');let body;
    if(req.url==='/healthz')body={status:'ok'};
    else if(req.url==='/api/v1/capabilities')body={management:false,client_api_version:1,search_modes:['literal','advanced'],search_filters:['source','format','path_prefix','title_contains'],pdf_title_only_evidence:true};
    else if(req.url==='/api/v1/sources')body={sources:inventoryList};
    else if(req.url.startsWith('/api/v1/search?')){const q=new URL(req.url,'http://127.0.0.1').searchParams.get('q');if(q==='slow'){slowSearchStarted();await slowSearchGate}currentResult=q==='title-only'?{...result,page:undefined,uri:'file:///tmp/synthetic/runbook.pdf'}:result;body={query:q,total:1,results:[currentResult]}}
    else if(req.url.startsWith('/api/v1/documents/doc_test')){const page=Number(new URL(req.url,'http://127.0.0.1').searchParams.get('page'));if(page===2&&blockPage2){page2Started();await page2Gate}body={id:currentResult.id,title:currentResult.title,uri:currentResult.uri,path:currentResult.path,source_id:inventory.id,source_name:inventory.name,source_kind:inventory.kind,media_type:'application/pdf',content_hash:'synthetic-hash',modified_at:'2026-10-09T00:00:00Z',page_count:2,text:page===0?'Title-only PDF evidence':'PDF evidence page '+page,truncated:true};if(page!==0)body.page=page}
    else{res.statusCode=404;body={error:'not found'}}res.end(JSON.stringify(body))
   });
 await new Promise(r=>server.listen(0,'127.0.0.1',r));const oldInput=vscode.window.showInputBox,oldPick=vscode.window.showQuickPick;let copied='',lastInputValue;
 try {
  await vscode.workspace.getConfiguration('findrail').update('endpoint',`http://127.0.0.1:${server.address().port}`,vscode.ConfigurationTarget.Global);
  vscode.window.showInputBox=async options=>{lastInputValue=options?.value;if(overrideQuery!==undefined){const value=overrideQuery;overrideQuery=undefined;return value}return options?.value??'webhook'};vscode.window.showQuickPick=async items=>items[0];
  await vscode.commands.executeCommand('findrail.connect');await vscode.commands.executeCommand('findrail.search');
  let editor=vscode.window.activeTextEditor;assert.ok(editor);assert.equal(editor.document.uri.scheme,'findrail-evidence');assert.match(editor.document.getText(),/PDF evidence page 1/);assert.match(editor.document.getText(),/Index snapshot: 2026-10-10/);
  await vscode.commands.executeCommand('findrail.nextPage');editor=vscode.window.activeTextEditor;assert.match(editor.document.getText(),/PDF evidence page 2/);assert.equal(editor.document.uri.fragment,'page=2');
  const evidenceOffset=editor.document.getText().indexOf('PDF evidence page 2');editor.selection=new vscode.Selection(editor.document.positionAt(evidenceOffset),editor.document.positionAt(evidenceOffset+10));
  await vscode.commands.executeCommand('findrail.copyCitation');copied=await vscode.env.clipboard.readText();assert.match(copied,/Selected passage from a truncated indexed preview\./);assert.match(copied,/page 2/);assert.match(copied,/PDF eviden/);
  await vscode.commands.executeCommand('findrail.copyLocation');copied=await vscode.env.clipboard.readText();assert.match(copied,/#page=2$/);
   await vscode.commands.executeCommand('findrail.searchSelection');assert.equal(lastInputValue,'PDF eviden','selection is shown for user confirmation before the search request');assert.ok(requestsSeen.some(x=>new URL(x.slice(4),'http://127.0.0.1').searchParams.get('q')==='PDF eviden'));
   overrideQuery='slow';const oldSearch=vscode.commands.executeCommand('findrail.search');await slowSearchReady;overrideQuery='webhook';await vscode.commands.executeCommand('findrail.search');const latestEditor=vscode.window.activeTextEditor,latestURI=latestEditor.document.uri.toString();releaseSlowSearch();await oldSearch;assert.equal(vscode.window.activeTextEditor.document.uri.toString(),latestURI,'late older search cannot replace newer evidence');
   overrideQuery='title-only';await vscode.commands.executeCommand('findrail.search');editor=vscode.window.activeTextEditor;assert.match(editor.document.getText(),/PDF evidence: title-only \(no page claim\)/);assert.doesNotMatch(editor.document.getText(),/PDF page: 0/);assert.equal(editor.document.uri.fragment,'');assert.ok(requestsSeen.includes('GET /api/v1/documents/doc_test?page=0'));
  await vscode.commands.executeCommand('findrail.nextPage');editor=vscode.window.activeTextEditor;assert.match(editor.document.getText(),/PDF evidence page 1/);assert.equal(editor.document.uri.fragment,'page=1');
   assert.ok(requestsSeen.includes('GET /api/v1/documents/doc_test?page=1'));assert.ok(requestsSeen.includes('GET /api/v1/documents/doc_test?page=2'));assert.ok(requestsSeen.every(x=>x.startsWith('GET ')&&!x.includes('/session')),'client uses only fixed read-only GET routes');
   blockPage2=true;const delayedEvidence=vscode.commands.executeCommand('findrail.nextPage');await page2Ready;await vscode.commands.executeCommand('findrail.disconnect');releasePage2();await delayedEvidence;assert.match(vscode.window.activeTextEditor.document.getText(),/PDF evidence page 1/,'late evidence cannot replace a preview after disconnect');
   await vscode.commands.executeCommand('findrail.connect');await vscode.commands.executeCommand('findrail.chooseSource');overrideQuery='webhook';await vscode.commands.executeCommand('findrail.search');editor=vscode.window.activeTextEditor;
   const beforeRemoval=await vscode.env.clipboard.readText();inventoryList=[];await vscode.commands.executeCommand('findrail.refreshSources');assert.match(editor.document.getText(),/PDF evidence page 1/,'already delivered text remains visible in the open editor');await vscode.commands.executeCommand('findrail.copyLocation');assert.equal(await vscode.env.clipboard.readText(),beforeRemoval,'source removal disables future copy actions');
  } finally {vscode.window.showInputBox=oldInput;vscode.window.showQuickPick=oldPick;await new Promise(r=>server.close(r))}
  if(process.env.FINDRAIL_TEST_ENDPOINT){
   await vscode.workspace.getConfiguration('findrail').update('endpoint',process.env.FINDRAIL_TEST_ENDPOINT,vscode.ConfigurationTarget.Global);
    let selectedGitHub;overrideQuery='githubsynthetic pinned fixture phrase';vscode.window.showInputBox=async options=>{if(overrideQuery!==undefined){const value=overrideQuery;overrideQuery=undefined;return value}return options?.value??'webhook'};vscode.window.showQuickPick=async items=>{const github=items.find(item=>item.source?.kind==='github');if(github){selectedGitHub=github.source;return github}return items[0]};
   await vscode.commands.executeCommand('findrail.connect');await vscode.commands.executeCommand('findrail.search');
   const actualEditor=vscode.window.activeTextEditor;assert.equal(actualEditor.document.uri.scheme,'findrail-evidence');assert.match(actualEditor.document.getText(),/Index snapshot:/);assert.match(actualEditor.document.getText(),/Original location: https:\/\/github\.com\/Example\/Demo\/blob\/a{40}\/docs\/github-note\.md/);assert.match(actualEditor.document.getText(),/githubsynthetic pinned fixture phrase/);assert.doesNotMatch(actualEditor.document.getText(),/no longer available/);
   await vscode.commands.executeCommand('findrail.copyCitation');const lastCitation=await vscode.env.clipboard.readText();assert.match(lastCitation,/github\.com\/Example\/Demo\/blob\/a{40}\/docs\/github-note\.md/);
   await vscode.commands.executeCommand('findrail.refreshEvidence');assert.match(vscode.window.activeTextEditor.document.getText(),/Index snapshot:/);
   execFileSync(process.env.FINDRAIL_TEST_BINARY,['forget','--data-dir',process.env.FINDRAIL_TEST_INDEX,selectedGitHub.id],{stdio:'ignore'});
   await vscode.commands.executeCommand('findrail.refreshSources');assert.match(vscode.window.activeTextEditor.document.getText(),/githubsynthetic pinned fixture phrase/,'previously delivered source text remains visible after external removal');await vscode.commands.executeCommand('findrail.copyCitation');assert.equal(await vscode.env.clipboard.readText(),lastCitation,'removed-source evidence cannot be copied again');
    await vscode.commands.executeCommand('findrail.disconnect');
    await vscode.commands.executeCommand('findrail.connect');let selectedArchive;vscode.window.showQuickPick=async items=>{const archive=items.find(item=>item.source?.kind==='archive');if(archive){selectedArchive=archive.source;assert.match(archive.label,/frozen archive/);return archive}return items[0]};await vscode.commands.executeCommand('findrail.chooseSource');assert.equal(selectedArchive?.kind,'archive');overrideQuery='webhook';await vscode.commands.executeCommand('findrail.search');assert.match(vscode.window.activeTextEditor.document.getText(),/Source: Frozen synthetic archive \([^;]+; archive\)/);await vscode.commands.executeCommand('findrail.disconnect');
  }
  const implementation=fs.readFileSync(path.join(ext.extensionPath,'src','extension.ts'),'utf8');assert.doesNotMatch(implementation,/globalState|workspaceState/,'extension does not persist queries, inventory, results, or evidence');
  console.log('Findrail VS Code extension-host assertions passed');
};
