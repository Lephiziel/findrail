import test from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import { endpoint, Client } from '../dist/protocol.cjs';
import { formatCitation } from '../dist/citation.mjs';

test('endpoint allowlist rejects unsafe authorities and URL components',()=>{
 for(const v of ['https://127.0.0.1','http://127.0.0.2','http://0.0.0.0','http://user@localhost','http://localhost/path','http://localhost/?q=x','http://example.org']) assert.throws(()=>endpoint(v));
 for(const v of ['http://127.0.0.1:7766','http://localhost','http://[::1]:7766']) assert.doesNotThrow(()=>endpoint(v));
});
test('requires compatible API and restricts requests to fixed read-only GETs',async()=>{
 const requested=[];const s=http.createServer((req,res)=>{requested.push(`${req.method} ${req.url}`);res.setHeader('content-type','application/json');res.end(JSON.stringify(req.url==='/healthz'?{status:'ok'}:{management:false,client_api_version:1,search_modes:['literal','advanced'],search_filters:['source','format','path_prefix','title_contains'],pdf_title_only_evidence:true,diagnostics:{extra:true}}))});
 await new Promise(r=>s.listen(0,'127.0.0.1',r));try{const c=new Client(`http://127.0.0.1:${s.address().port}`,5);await c.connect();assert.deepEqual(requested,['GET /healthz','GET /api/v1/capabilities']);await assert.rejects(()=>c['get']('/api/v1/session',65536),/route_denied/);assert.equal(requested.length,2)}finally{s.close()}
});
test('rejects redirects and invalid capability contract',async()=>{
 const s=http.createServer((req,res)=>{if(req.url==='/healthz'){res.setHeader('content-type','application/json');res.end('{"status":"ok"}')}else{res.statusCode=302;res.setHeader('location','http://example.org');res.end()}});await new Promise(r=>s.listen(0,'127.0.0.1',r));try{await assert.rejects(()=>new Client(`http://127.0.0.1:${s.address().port}`,5).connect(),/http_rejected/)}finally{s.close()}
});
test('the shipped citation formatter preserves PDF page, exact excerpt, and truncation provenance',()=>{
 const citation=formatCitation({title:'Runbook',uri:'file:///tmp/runbook.pdf#page=2',media_type:'application/pdf',page:2,text:'line one\n```\nline three',truncated:true},{excerpt:'line one'});
 assert.match(citation,/Runbook — page 2/);assert.match(citation,/Selected passage from a truncated indexed preview\./);assert.match(citation,/Source: \[Runbook, page 2\]/);
 assert.throws(()=>formatCitation({title:'bad',uri:'https://example.test/x',media_type:'text/plain',text:'x',truncated:false}));
});
test('bounds chunked response bytes, rejects malformed UTF-8, and releases canceled request slots',async()=>{
 let mode='large';const s=http.createServer((req,res)=>{if(mode==='large'){res.setHeader('content-type','application/json');res.write('{"x":"');res.end('a'.repeat(5000)+'"}')}else if(mode==='utf8'){res.setHeader('content-type','application/json');res.end(Buffer.from([0xc3,0x28]))}else{res.setHeader('content-type','application/json')}});
 await new Promise(r=>s.listen(0,'127.0.0.1',r));try{const c=new Client(`http://127.0.0.1:${s.address().port}`,1);await assert.rejects(()=>c.get('/healthz',1024),/oversize|transport/);mode='utf8';await assert.rejects(()=>c.get('/healthz',1024),/invalid_json/);mode='hold';const controller=new AbortController();const pending=c.get('/healthz',1024,controller.signal);await new Promise(r=>setTimeout(r,20));controller.abort();await assert.rejects(()=>pending,/canceled/);assert.equal(c.active,0)}finally{s.close()}
});
test('rejects search results that escape the selected source',async()=>{
 const s=http.createServer((req,res)=>{res.setHeader('content-type','application/json');res.end(JSON.stringify({total:1,results:[{id:'doc_1',title:'title',uri:'file:///tmp/a',path:'a',source_id:'other',source_name:'s',source_kind:'filesystem',snippet:'x',score:1,media_type:'text/plain'}]}))});await new Promise(r=>s.listen(0,'127.0.0.1',r));try{const c=new Client(`http://127.0.0.1:${s.address().port}`,1);await assert.rejects(()=>c.search('q','chosen','literal',{},20),/result_invalid/)}finally{s.close()}
});
test('enforces a deadline and releases its slot',async()=>{
 const s=http.createServer((req,res)=>{res.setHeader('content-type','application/json')});await new Promise(r=>s.listen(0,'127.0.0.1',r));try{const c=new Client(`http://127.0.0.1:${s.address().port}`,0.05);await assert.rejects(()=>c.get('/healthz',1024),/deadline/);assert.equal(c.active,0)}finally{s.close()}
});
