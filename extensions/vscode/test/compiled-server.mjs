import { mkdtemp, cp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { spawn, execFileSync } from 'node:child_process';
import net from 'node:net';
import { once } from 'node:events';
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { Client } from '../dist/protocol.cjs';

const repo=resolve('../..');
const dir=await mkdtemp(join(tmpdir(),'findrail-vscode-integration-'));
let server;
try {
 const binary=join(dir,process.platform==='win32'?'findrail.exe':'findrail');
 execFileSync('go',['build','-o',binary,'./cmd/findrail'],{cwd:repo,stdio:'inherit'});
 const documents=join(dir,'synthetic-docs');await cp(join(repo,'examples/demo'),documents,{recursive:true});
 execFileSync('go',['run','./extensions/vscode/test/make_docx',join(documents,'synthetic-retrieval.docx')],{cwd:repo,stdio:'inherit'});
 const data=join(dir,'index');execFileSync(binary,['index','--data-dir',data,'--max-docx-bytes','8388608',documents],{cwd:repo,stdio:'inherit'});
 const sourceJSON=JSON.parse(execFileSync(binary,['sources','--data-dir',data,'--json'],{cwd:repo,encoding:'utf8'}));const fsSource=sourceJSON.sources[0].id;
 const snapshot=join(dir,'synthetic.findrail.zip');execFileSync(binary,['export-source','--data-dir',data,'--source',fsSource,'--output',snapshot,'--json'],{cwd:repo,stdio:'inherit'});
 execFileSync(binary,['import-source','--data-dir',data,'--name','Frozen synthetic archive','--json',snapshot],{cwd:repo,stdio:'inherit'});
 execFileSync('go',['run','./extensions/vscode/test/make_github',data],{cwd:repo,stdio:'inherit'});
 const allSources=JSON.parse(execFileSync(binary,['sources','--data-dir',data,'--json'],{cwd:repo,encoding:'utf8'})).sources;const archiveSource=allSources.find(s=>s.kind==='archive')?.id;assert.ok(archiveSource,'synthetic frozen archive source imported');
 const database=join(data,'findrail.db');const beforeHash=createHash('sha256').update(await (await import('node:fs/promises')).readFile(database)).digest('hex');
 const probe=net.createServer();probe.listen(0,'127.0.0.1');await once(probe,'listening');const port=probe.address().port;await new Promise((r,j)=>probe.close(e=>e?j(e):r()));
 server=spawn(binary,['serve','--data-dir',data,'--addr',`127.0.0.1:${port}`,'--no-sync'],{cwd:repo,stdio:['ignore','ignore','ignore']});
 const client=new Client(`http://127.0.0.1:${port}`,5);
 let connected=false;for(let i=0;i<50&&!connected;i++){try{await client.connect();connected=true}catch{await new Promise(r=>setTimeout(r,100))}}
 assert.ok(connected,'compiled synthetic Findrail server starts');
 const sources=await client.sources();assert.equal(sources.length,3);assert.ok(sources.some(s=>s.id===fsSource&&s.kind==='filesystem'));assert.ok(sources.some(s=>s.id===archiveSource&&s.kind==='archive'));const githubSource=sources.find(s=>s.kind==='github')?.id;assert.ok(githubSource,'offline GitHub fixture source published');
 const literal=await client.search('webhook',fsSource,'literal',{},20);assert.ok(literal.total>0);assert.ok(literal.results.every(r=>r.source_id===fsSource));
 const advanced=await client.search('webhook OR retry',fsSource,'advanced',{format:'pdf'},20);assert.ok(advanced.results.every(r=>r.media_type==='application/pdf'));
 const docx=await client.search('retrieval docx fixture phrase',fsSource,'literal',{format:'docx'},20);assert.ok(docx.results.length>0);assert.ok(docx.results.every(r=>r.media_type.includes('wordprocessingml')));const docxEvidence=await client.evidence(docx.results[0],undefined);assert.equal(docxEvidence.source_id,fsSource);assert.equal(docxEvidence.page,undefined);assert.match(docxEvidence.text,/retrieval docx fixture phrase/);
 const pdf=literal.results.find(r=>r.media_type==='application/pdf');assert.ok(pdf,'synthetic demo includes indexed text PDF');
 assert.ok(pdf.page_count>=2,'synthetic PDF has at least two indexed pages');const page=await client.evidence({...pdf,page:2,page_count:pdf.page_count},2);assert.equal(page.id,pdf.id);assert.equal(page.source_id,fsSource);assert.equal(page.media_type,'application/pdf');assert.equal(page.page,2);
 const archived=await client.search('webhook',archiveSource,'literal',{},20);assert.ok(archived.results.length>0);assert.ok(archived.results.every(r=>r.source_id===archiveSource&&r.source_kind==='archive'));const archivePDF=archived.results.find(r=>r.media_type==='application/pdf');assert.ok(archivePDF);const archivedPage=await client.evidence({...archivePDF,page:2,page_count:archivePDF.page_count},2);assert.equal(archivedPage.source_id,archiveSource);assert.equal(archivedPage.page,2);
 const github=await client.search('githubsynthetic pinned fixture phrase',githubSource,'literal',{},20);assert.equal(github.results.length,1);assert.equal(github.results[0].source_kind,'github');assert.match(github.results[0].uri,/github\.com\/Example\/Demo\/blob\/a{40}\/docs\/github-note\.md/);const githubEvidence=await client.evidence(github.results[0],undefined);assert.match(githubEvidence.text,/githubsynthetic pinned fixture phrase/);assert.equal(githubEvidence.page,undefined);
 const afterHash=createHash('sha256').update(await (await import('node:fs/promises')).readFile(database)).digest('hex');assert.equal(afterHash,beforeHash,'read-only HTTP retrieval does not alter the indexed database');
 console.log('compiled Findrail server integration passed: filesystem, DOCX, offline GitHub fixture, frozen archive, Literal/Advanced filters, PDF page 2, DB unchanged');
} finally {
 if(server&&!server.killed){server.kill();await Promise.race([once(server,'exit'),new Promise(r=>setTimeout(r,2000))])}
 await rm(dir,{recursive:true,force:true});
}
