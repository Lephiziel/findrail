import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import vm from 'node:vm';

const html = readFileSync(new URL('./index.html', import.meta.url), 'utf8');
const script = [...html.matchAll(/<script type="module">([\s\S]*?)<\/script>/g)][1][1];
const flush = async () => { for (let i = 0; i < 6; i++) await new Promise(resolve => setImmediate(resolve)); };
const response = (data, status = 200) => new Response(JSON.stringify(data), {status, headers:{'Content-Type':'application/json'}});
const source = {id:'source-a',kind:'filesystem',name:'Synthetic',root:'/synthetic',documents:1,last_indexed_at:'2026-10-17T00:00:00Z',max_docx_bytes:0};
const summary = {status:'succeeded',format:'findrail-portable-snapshot',version:1,kind:'filesystem',documents:1,pages:0,bytes:1234,fingerprint:'a'.repeat(64),frozen:true,claim:'b'.repeat(64)};

async function harness(capabilities = {management:true,portable_snapshots:true}) {
  const nodes=new Map(),listeners=new Map(),timers=new Map(),calls=[];let nextTimer=0,sources=[structuredClone(source)],jobs=[];
  const on=(id,type,fn)=>{const key=id+':'+type;if(!listeners.has(key))listeners.set(key,[]);listeners.get(key).push(fn)};
  const emit=(id,type,event={})=>Promise.all((listeners.get(id+':'+type)||[]).map(fn=>fn(event)));
  let document;
  function node(tag,id='') {
    return {tag,tagName:tag.toUpperCase(),id,children:[],value:'',hidden:false,disabled:false,readOnly:false,open:false,isConnected:!!id,dataset:{},textContent:'',
      addEventListener(type,fn){on(id||tag,type,fn)},
      append(...items){this.children.push(...items);const connect=n=>{n.isConnected=this.isConnected;for(const child of n.children||[])connect(child)};for(const item of items){item.parentElement=this;connect(item)}},
      replaceChildren(...items){const detach=n=>{n.isConnected=false;for(const child of n.children||[])detach(child)};for(const child of this.children)detach(child);this.children=[];this.append(...items)},
      closest(selector){for(let n=this;n;n=n.parentElement)if(n.className===selector.slice(1))return n;return null},
      querySelector(selector){return this.querySelectorAll(selector)[0]||null},
      querySelectorAll(selector){const all=n=>(n.children||[]).flatMap(x=>[x,...all(x)]);return all(this).filter(x=>selector==='button'?x.tag==='button':x.className===selector.slice(1))},
      setAttribute(name,value){this[name]=value},
      focus(){if(this.isConnected&&!this.disabled)document.activeElement=this},
      showModal(){this.open=true;this.focus()},
      close(){this.open=false;emit(id,'close')},
    };
  }
  const element=id=>{if(!nodes.has(id)){const n=node('div',id);n.hidden=new RegExp('id="'+id+'"[^>]*\\bhidden\\b').test(html);nodes.set(id,n)}return nodes.get(id)};
  document={hidden:false,getElementById:element,createElement:tag=>node(tag),addEventListener:(type,fn)=>on('document',type,fn),querySelector:()=>element('management-head')};
  document.activeElement=element('body');
  const schedule=(fn,delay)=>{const id=++nextTimer;timers.set(id,{fn,delay});return id};
  const window={findrailBootstrap:Promise.resolve({capabilities,token:'synthetic-token'}),setTimeout:schedule,clearTimeout:id=>timers.delete(id),addEventListener:(type,fn)=>on('window',type,fn),dispatchEvent:event=>{emit('window',event.type,event)}};
  const routes=[];
  const context=vm.createContext({window,document,TextEncoder,TextDecoder,Uint8Array,AbortController,Event,CustomEvent:class extends Event{constructor(type,{detail}){super(type);this.detail=detail}},setTimeout:schedule,clearTimeout:id=>timers.delete(id),
    async fetch(url,options={}){calls.push({url,options,body:options.body?JSON.parse(options.body):undefined});const route=routes.find(x=>x.match(url,options));if(route)return route.fn(url,options);
      if(url==='/api/v1/sources')return response({sources});if(url==='/api/v1/sync')return response({sources:[],enabled:true});if(url==='/api/v1/jobs')return response({jobs});throw Error('Unexpected request '+url)},
  });
  await vm.runInContext(`(async()=>{${script};globalThis.openSnapshot=openPortable;globalThis.reloadSources=loadSources})()`,context);await flush();
  const h={element,context,document,window,calls,timers,emit,
    route(match,fn){routes.unshift({match:typeof match==='string'?url=>url===match:match,fn})},
    setSources(value){sources=value},setJobs(value){jobs=value},
    async open(mode='inspect',value=null){element('snapshot-inspect-open').focus();context.openSnapshot(mode,value,document.activeElement);await flush()},
    async submit(){const promise=emit('snapshot-transfer-form','submit',{preventDefault(){},currentTarget:element('snapshot-transfer-form')});await flush();return promise},
    async click(id){const promise=emit(id,'click',{currentTarget:element(id)});await flush();return promise},
    async input(id,value){element(id).value=value;await emit(id,'input');await flush()},
    async tick(delay){const entry=[...timers].findLast(([,x])=>x.delay===delay);assert.ok(entry,'missing timer '+delay);timers.delete(entry[0]);await entry[1].fn();await flush()},
  };return h;
}

function inspection(h, result=summary) {
  h.route('/api/v1/snapshots/inspect',()=>response({job:{id:'inspect-1',type:'snapshot-inspect'}} ,202));
  h.route('/api/v1/snapshots/inspections/inspect-1',()=>response(result));
}
async function preview(h) {inspection(h);await h.open();await h.input('snapshot-path','/synthetic/archive.zip');await h.submit();await flush()}

test('snapshot controls require both writable management and the optional capability',async()=>{
  for(const capabilities of [{management:true},{management:true,portable_snapshots:false},{management:false,portable_snapshots:true}]){
    const h=await harness(capabilities);await h.open();assert.equal(h.element('snapshot-transfer').open,false);assert.equal(h.element('snapshot-inspect-open').hidden,true);
  }
  const h=await harness();await h.open();assert.equal(h.element('snapshot-transfer').open,true);
});

test('late inspection cannot alter a new dialog or install its old claim',async()=>{
  const h=await harness();let release;
  inspection(h);h.route('/api/v1/snapshots/inspections/inspect-1',()=>new Promise(resolve=>{release=()=>resolve(response(summary))}));
  await h.open();await h.input('snapshot-path','/synthetic/old.zip');await h.submit();assert.ok(release);
  await h.click('snapshot-close');await h.open();await h.input('snapshot-path','/synthetic/new.zip');const before=h.element('snapshot-summary').textContent;
  release();await flush();assert.equal(h.element('snapshot-summary').textContent,before);assert.equal(h.element('snapshot-path').value,'/synthetic/new.zip');assert.notEqual(h.element('snapshot-submit').textContent,'Import frozen source');
});

test('path is locked during submission and polling; changing a preview invalidates its claim',async()=>{
  const h=await harness();inspection(h,{status:'running'});await h.open();await h.input('snapshot-path','/synthetic/archive.zip');await h.submit();
  assert.equal(h.element('snapshot-path').readOnly,true);
  h.route('/api/v1/snapshots/inspections/inspect-1',()=>response(summary));await h.tick(500);
  await h.input('snapshot-path','/synthetic/other.zip');assert.equal(h.element('snapshot-submit').textContent,'Inspect snapshot');assert.equal(h.element('snapshot-submit').disabled,false);
});

test('validated inspection focuses the name; rejected or expired import requires a new inspection',async()=>{
  const h=await harness();await preview(h);assert.equal(h.document.activeElement.id,'snapshot-name');
  h.route('/api/v1/snapshots/import',()=>response({error:'invalid_claim'},400));await h.input('snapshot-name','Frozen notes');await h.submit();
  assert.equal(h.element('snapshot-submit').textContent,'Inspect snapshot');assert.equal(h.element('snapshot-submit').hidden,false);assert.equal(h.element('snapshot-path').readOnly,false);
  await h.submit();assert.equal(h.calls.filter(c=>c.url==='/api/v1/snapshots/inspect').length,2);
});

test('an unreadable successful import response is uncertain and cannot be resubmitted',async()=>{
  const h=await harness();await preview(h);h.route('/api/v1/snapshots/import',()=>new Response('{',{status:202}));await h.input('snapshot-name','Frozen notes');await h.submit();await h.submit();
  assert.equal(h.calls.filter(c=>c.url==='/api/v1/snapshots/import').length,1);assert.equal(h.calls.filter(c=>c.url==='/api/v1/snapshots/inspect').length,1);
  assert.match(h.element('snapshot-summary').textContent,/unknown|uncertain/i);assert.equal(h.element('snapshot-submit').disabled,true);
  assert.ok(h.calls.filter(c=>c.url==='/api/v1/sources').length>1,'uncertain commit must reconcile inventory');
});

test('a source removed and re-added cannot reuse an old export confirmation',async()=>{
  const h=await harness();await h.open('export',source);await h.input('snapshot-path','/synthetic/new.zip');
  await h.emit('window','source-removed',{detail:source.id});h.setSources([source]);await flush();await h.submit();
  assert.equal(h.calls.filter(c=>c.url.endsWith('/snapshot-export')).length,0);assert.match(h.element('snapshot-summary').textContent,/source.*(changed|removed)/i);
});

test('closing an export returns focus to the current rebuilt source card',async()=>{
  const h=await harness();const exportButton=()=>h.element('source-list').children[0].querySelectorAll('button').find(b=>b.textContent==='Export snapshot');
  exportButton().focus();h.context.openSnapshot('export',source,h.document.activeElement);await flush();await h.context.reloadSources();await h.click('snapshot-close');
  assert.equal(h.document.activeElement,exportButton());await h.context.reloadSources();assert.equal(h.document.activeElement,exportButton(),'inventory refresh must preserve the returned action focus');
});

test('a canceled old request cannot replace a new dialog error or focus',async()=>{
  const h=await harness();inspection(h,{status:'running'});await h.open();await h.input('snapshot-path','/synthetic/archive.zip');await h.submit();let reject;
  h.route('/api/v1/jobs/inspect-1/cancel',()=>new Promise((resolve,rejectPromise)=>{reject=rejectPromise}));
  const pending=h.click('snapshot-cancel-job');await flush();await h.click('snapshot-close');await h.open();const text=h.element('snapshot-summary').textContent;
  reject(Error('old cancellation response'));await pending;await flush();assert.equal(h.element('snapshot-summary').textContent,text);
});

test('one pending submission blocks duplicates and is reconciled after closing',async()=>{
  const h=await harness();let finish;
  h.route('/api/v1/snapshots/inspect',(url,options)=>new Promise(resolve=>{finish=()=>resolve(response({job:{id:'inspect-late'}},202))}));
  await h.open();await h.input('snapshot-path','/synthetic/archive.zip');const pending=h.submit();await flush();await h.submit();
  assert.equal(h.calls.filter(c=>c.url==='/api/v1/snapshots/inspect').length,1);assert.equal(h.element('snapshot-path').readOnly,true);
  const request=h.calls.find(c=>c.url==='/api/v1/snapshots/inspect');await h.click('snapshot-close');assert.equal(request.options.signal.aborted,true);
  await h.open();assert.equal(h.element('snapshot-submit').disabled,true);finish();await pending;await flush();
  assert.notEqual(h.element('snapshot-submit').textContent,'Import frozen source');assert.equal(h.element('snapshot-check').hidden,false);
});

test('a hidden tab pauses polling and explicit status check resumes without a second POST',async()=>{
  const h=await harness();let finish;
  inspection(h);h.route('/api/v1/snapshots/inspections/inspect-1',()=>new Promise(resolve=>{finish=()=>resolve(response(summary))}));
  await h.open();await h.input('snapshot-path','/synthetic/archive.zip');await h.submit();
  h.document.hidden=true;await h.emit('document','visibilitychange');finish();await flush();
  assert.equal(h.element('snapshot-name').hidden,true);assert.equal(h.element('snapshot-submit').disabled,true);assert.equal(h.element('snapshot-check').hidden,false);
  h.document.hidden=false;await h.emit('document','visibilitychange');h.route('/api/v1/snapshots/inspections/inspect-1',()=>response(summary));await h.click('snapshot-check');await flush();
  assert.equal(h.element('snapshot-submit').textContent,'Import frozen source');assert.equal(h.calls.filter(c=>c.url==='/api/v1/snapshots/inspect').length,1);
});

test('export completion verifies the new path once and returns focus to Close',async()=>{
  const h=await harness();h.route('/api/v1/sources/source-a/snapshot-export',()=>response({job:{id:'export-1'}},202));
  h.setJobs([{id:'export-1',type:'snapshot-export',status:'succeeded',result:'exported'}]);inspection(h);
  const outputPath=String.raw`C:\Users\Alex\Archives\new snapshot résumé 雪.zip`;
  await h.open('export',source);
  assert.equal(h.element('snapshot-transfer-form').dataset.mode,'export');
  assert.equal(h.element('snapshot-path').placeholder,'New output archive path on the Findrail computer');
  assert.ok(h.element('snapshot-transfer-help').textContent.includes('Browser downloads for Markdown citations and evidence bundles are separate actions.'));
  for(const example of ['Windows: C:\\Users\\Alex\\Archives\\new snapshot.zip','macOS: /Users/alex/Archives/new snapshot.zip','Linux: /home/alex/Archives/new snapshot.zip'])assert.ok(h.element('snapshot-transfer-help').textContent.includes(example));
  await h.input('snapshot-path',outputPath);await h.submit();await flush();
  assert.match(h.element('snapshot-summary').textContent,/Snapshot exported and verified.*1234.*fingerprint/);
  assert.equal(h.document.activeElement.id,'snapshot-close');assert.equal(h.element('snapshot-submit').hidden,true);
  assert.deepEqual(h.calls.find(c=>c.url.endsWith('/snapshot-export')).body,{output_path:outputPath});
  assert.equal(h.calls.find(c=>c.url==='/api/v1/snapshots/inspect').body.path,outputPath);
});

test('inspect and import modes keep the literal host path and retain it with an honest failed result',async()=>{
  const h=await harness(),archivePath=String.raw`C:\Users\Alex\Archives\Team Notes résumé 雪.zip`;
  inspection(h);await h.open();
  assert.equal(h.element('snapshot-transfer-form').dataset.mode,'inspect');
  assert.equal(h.element('snapshot-path').placeholder,'Existing archive path on the Findrail computer');
  assert.equal(h.element('snapshot-path-label').textContent,'Absolute path to an existing archive on the machine running Findrail');
  for(const example of ['Windows: C:\\Users\\Alex\\Archives\\notes archive.zip','macOS: /Users/alex/Archives/notes archive.zip','Linux: /home/alex/Archives/notes archive.zip'])assert.ok(h.element('snapshot-transfer-help').textContent.includes(example));
  await h.input('snapshot-path',archivePath);await h.submit();await flush();
  assert.deepEqual(h.calls.find(c=>c.url==='/api/v1/snapshots/inspect').body,{path:archivePath});
  assert.equal(h.element('snapshot-path').value,archivePath,'inspection must not rewrite or clear the entered path');
  assert.equal(h.element('snapshot-transfer-form').dataset.mode,'import');
  assert.equal(h.element('snapshot-path').placeholder,'Path to the inspected archive');
  assert.equal(h.element('snapshot-submit').textContent,'Import frozen source');
  assert.match(h.element('snapshot-transfer-help').textContent,/exact inspected archive path/);

  const hostileError='Archive content changed after inspection; no source was imported. <img src=x onerror=alert(1)>';
  h.setJobs([{id:'import-1',type:'snapshot-import',status:'failed',error:hostileError}]);
  h.route('/api/v1/snapshots/import',()=>response({job:{id:'import-1'}},202));
  await h.input('snapshot-name','Team Notes');await h.submit();await flush();
  assert.equal(h.element('snapshot-path').value,archivePath,'a failed import must retain the user path');
  assert.equal(h.element('snapshot-name').value,'Team Notes','a failed import must retain the entered source name');
  assert.equal(h.element('snapshot-transfer-form').dataset.mode,'inspect');
  assert.equal(h.element('snapshot-submit').textContent,'Inspect snapshot');
  assert.match(h.element('snapshot-summary').textContent,/Archive content changed/);
  assert.ok(h.element('snapshot-summary').textContent.includes('<img src=x onerror=alert(1)>'));
  assert.equal(h.element('snapshot-summary').children.length,0,'server error text must not create markup');
});

test('closing before an export result prevents a late automatic metadata inspection',async()=>{
  const h=await harness();let finish;h.route('/api/v1/sources/source-a/snapshot-export',()=>response({job:{id:'export-1'}},202));await h.open('export',source);
  h.route('/api/v1/jobs',()=>new Promise(resolve=>{finish=()=>resolve(response({jobs:[{id:'export-1',status:'succeeded',result:'exported'}]}))}));
  await h.input('snapshot-path','/synthetic/new.zip');await h.submit();await h.click('snapshot-close');finish();await flush();
  assert.equal(h.calls.filter(c=>c.url==='/api/v1/snapshots/inspect').length,0);
});

test('duplicate import identifies the existing source and preserves collection and form state',async()=>{
  const h=await harness();await preview(h);
  const frozen={...source,id:'archive-original',kind:'archive',name:'Original existing name',archive:{origin:{kind:'filesystem'},original_indexed_at:'2026-10-17T00:00:00Z',imported_at:'2026-10-17T01:00:00Z'}};
  h.setSources([source,frozen]);h.setJobs([{id:'import-1',type:'snapshot-import',status:'succeeded',result:'already_imported:'+frozen.id}]);
  const evidence={entries:[{text:'exact pinned quote',note:'unsaved note',key:'first'}],generation:3};h.window.localEvidenceCollection=evidence;h.element('query').value='current query';
  h.route('/api/v1/snapshots/import',()=>response({job:{id:'import-1'}},202));await h.input('snapshot-name','Attempted replacement name');await h.submit();await flush();
  assert.match(h.element('snapshot-summary').textContent,/already imported as.*Original existing name.*existing name was preserved/);
  assert.equal(h.element('query').value,'current query');assert.equal(h.window.localEvidenceCollection,evidence);assert.equal(evidence.entries[0].note,'unsaved note');
});

test('changed archive failure consumes the old claim and permits only a new inspection',async()=>{
  const h=await harness();await preview(h);h.setJobs([{id:'import-1',type:'snapshot-import',status:'failed',error_code:'snapshot_changed',error:'Archive content changed after inspection; no source was imported.'}]);
  h.route('/api/v1/snapshots/import',()=>response({job:{id:'import-1'}},202));await h.input('snapshot-name','Frozen notes');await h.submit();await flush();
  assert.match(h.element('snapshot-summary').textContent,/Archive content changed/);assert.equal(h.element('snapshot-submit').textContent,'Inspect snapshot');
  await h.submit();assert.equal(h.calls.filter(c=>c.url==='/api/v1/snapshots/import').length,1);assert.equal(h.calls.filter(c=>c.url==='/api/v1/snapshots/inspect').length,2);
});

test('local path/name bounds preserve exact Unicode and do not submit invalid inputs',async()=>{
  const h=await harness();inspection(h);await h.open();
  for(const path of ['relative.zip','C:relative.zip','/'+ 'я'.repeat(2048),'/bad\0path','/bad\ud800path']){await h.input('snapshot-path',path);await h.submit()}
  assert.equal(h.calls.filter(c=>c.url==='/api/v1/snapshots/inspect').length,0);
  await h.input('snapshot-path','/synthetic/Точный путь.zip ');await h.submit();assert.equal(h.calls.find(c=>c.url==='/api/v1/snapshots/inspect').body.path,'/synthetic/Точный путь.zip ');
  for(const name of ['','  ','я'.repeat(257),'bad\0name','bad\ud800name']){await h.input('snapshot-name',name);await h.submit()}
  assert.equal(h.calls.filter(c=>c.url==='/api/v1/snapshots/import').length,0);
});

test('unreadable or oversized responses cannot enable Import and each request is bounded',async()=>{
  const h=await harness();inspection(h);h.route('/api/v1/snapshots/inspections/inspect-1',()=>new Response('x'.repeat(1048577),{status:200}));
  await h.open();await h.input('snapshot-path','/synthetic/archive.zip');await h.submit();await flush();
  assert.equal(h.element('snapshot-name').hidden,true);assert.equal(h.element('snapshot-submit').disabled,true);assert.match(h.element('snapshot-summary').textContent,/byte limit/);
  await h.click('snapshot-close');
  const next=await harness();next.route('/api/v1/snapshots/inspect',(url,options)=>new Promise((resolve,reject)=>options.signal.addEventListener('abort',()=>reject(Error('request deadline')))));
  await next.open();await next.input('snapshot-path','/synthetic/archive.zip');const pending=next.submit();await flush();await next.tick(8000);await pending;
  assert.match(next.element('snapshot-summary').textContent,/unknown/);assert.equal(next.element('snapshot-submit').disabled,true);
});

test('Cancel is one request at a time and never conceals a committed success',async()=>{
  const h=await harness();inspection(h,{status:'running'});await h.open();await h.input('snapshot-path','/synthetic/archive.zip');await h.submit();let finish;
  h.route('/api/v1/jobs/inspect-1/cancel',()=>new Promise(resolve=>{finish=()=>resolve(new Response(null,{status:204}))}));
  const pending=h.click('snapshot-cancel-job');await flush();await h.click('snapshot-cancel-job');assert.equal(h.calls.filter(c=>c.url.endsWith('/cancel')).length,1);
  h.route('/api/v1/snapshots/inspections/inspect-1',()=>response(summary));await h.tick(500);const text=h.element('snapshot-summary').textContent;finish();await pending;
  assert.equal(h.element('snapshot-summary').textContent,text);assert.equal(h.element('snapshot-submit').textContent,'Import frozen source');
});

test('pagehide aborts monitoring and late responses cannot restore the dialog',async()=>{
  const h=await harness();let finish;inspection(h);h.route('/api/v1/snapshots/inspections/inspect-1',()=>new Promise(resolve=>{finish=()=>resolve(response(summary))}));
  await h.open();await h.input('snapshot-path','/synthetic/archive.zip');await h.submit();await h.emit('window','pagehide');finish();await flush();
  assert.equal(h.element('snapshot-name').hidden,true);assert.equal(h.calls.find(c=>c.url.endsWith('/inspections/inspect-1')).options.signal.aborted,true);
});

test('expired inspection and a running operation past the deadline require explicit recovery',async()=>{
  const h=await harness();inspection(h);h.route('/api/v1/snapshots/inspections/inspect-1',()=>response({status:'expired',error:'Inspection expired.'},410));
  await h.open();await h.input('snapshot-path','/synthetic/archive.zip');await h.submit();await flush();
  assert.match(h.element('snapshot-summary').textContent,/expired/);assert.equal(h.element('snapshot-submit').textContent,'Inspect snapshot');assert.equal(h.element('snapshot-name').hidden,true);
  const next=await harness();let now=1000;next.context.Date=class extends Date{static now(){return now}};
  inspection(next,{status:'running'});await next.open();await next.input('snapshot-path','/synthetic/archive.zip');await next.submit();now+=135001;await next.tick(500);
  assert.match(next.element('snapshot-summary').textContent,/time limit/);assert.equal(next.element('snapshot-submit').disabled,true);assert.equal(next.element('snapshot-check').hidden,false);
});

test('failed metadata verification preserves the already committed export outcome',async()=>{
  const h=await harness();h.route('/api/v1/sources/source-a/snapshot-export',()=>response({job:{id:'export-1'}},202));
  h.setJobs([{id:'export-1',type:'snapshot-export',status:'succeeded',result:'exported'}]);inspection(h);
  h.route('/api/v1/snapshots/inspections/inspect-1',()=>response({status:'failed',error:'Preview quota reached.'},409));
  await h.open('export',source);await h.input('snapshot-path','/synthetic/new.zip');await h.submit();await flush();
  assert.match(h.element('snapshot-summary').textContent,/Snapshot exported, but.*verification failed.*Preview quota/);
  assert.equal(h.element('snapshot-submit').hidden,true);assert.equal(h.document.activeElement.id,'snapshot-close');
  await h.submit();assert.equal(h.calls.filter(c=>c.url.endsWith('/snapshot-export')).length,1);
});
