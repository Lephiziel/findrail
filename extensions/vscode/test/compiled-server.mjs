import { mkdtemp, cp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { spawn, execFileSync } from 'node:child_process';
import net from 'node:net';
import { once } from 'node:events';
import assert from 'node:assert/strict';
import { Client } from '../dist/protocol.cjs';

const repo=resolve('../..');
const dir=await mkdtemp(join(tmpdir(),'findrail-vscode-integration-'));
let server;
try {
 const binary=join(dir,process.platform==='win32'?'findrail.exe':'findrail');
 execFileSync('go',['build','-o',binary,'./cmd/findrail'],{cwd:repo,stdio:'inherit'});
 const documents=join(dir,'synthetic-docs');await cp(join(repo,'examples/demo'),documents,{recursive:true});
 execFileSync(binary,['index','--data-dir',join(dir,'index'),documents],{cwd:repo,stdio:'inherit'});
 const probe=net.createServer();probe.listen(0,'127.0.0.1');await once(probe,'listening');const port=probe.address().port;await new Promise((r,j)=>probe.close(e=>e?j(e):r()));
 server=spawn(binary,['serve','--data-dir',join(dir,'index'),'--addr',`127.0.0.1:${port}`,'--no-sync'],{cwd:repo,stdio:['ignore','ignore','ignore']});
 const client=new Client(`http://127.0.0.1:${port}`,5);
 let connected=false;for(let i=0;i<50&&!connected;i++){try{await client.connect();connected=true}catch{await new Promise(r=>setTimeout(r,100))}}
 assert.ok(connected,'compiled synthetic Findrail server starts');
 const sources=await client.sources();assert.equal(sources.length,1);assert.equal(sources[0].kind,'filesystem');
 const literal=await client.search('webhook',sources[0].id,'literal',{},20);assert.ok(literal.total>0);assert.ok(literal.results.every(r=>r.source_id===sources[0].id));
 const advanced=await client.search('webhook OR retry',sources[0].id,'advanced',{format:'pdf'},20);assert.ok(advanced.results.every(r=>r.media_type==='application/pdf'));
 const pdf=literal.results.find(r=>r.media_type==='application/pdf');assert.ok(pdf,'synthetic demo includes indexed text PDF');
 assert.ok(pdf.page_count>=2,'synthetic PDF has at least two indexed pages');const page=await client.evidence({...pdf,page:2,page_count:pdf.page_count},2);assert.equal(page.id,pdf.id);assert.equal(page.source_id,sources[0].id);assert.equal(page.media_type,'application/pdf');assert.equal(page.page,2);
 console.log('compiled Findrail server integration passed: capabilities, inventory, Literal, Advanced/filter, PDF page 2 evidence');
} finally {
 if(server&&!server.killed){server.kill();await Promise.race([once(server,'exit'),new Promise(r=>setTimeout(r,2000))])}
 await rm(dir,{recursive:true,force:true});
}
