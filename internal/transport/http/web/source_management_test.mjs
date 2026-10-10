import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import vm from 'node:vm';

const html = readFileSync(new URL('./index.html', import.meta.url), 'utf8');
const script = [...html.matchAll(/<script type="module">([\s\S]*?)<\/script>/g)][0][1]
  .replace(/^import .*;$/m, '');
const managementScript = [...html.matchAll(/<script type="module">([\s\S]*?)<\/script>/g)][1][1]
  .replace(/^import .*;$/m, '');
const flush = () => new Promise(resolve => setImmediate(resolve));

function harness(sourcePayload = []) {
  const elements = new Map(), handlers = new Map(), timers = new Map();
  let nextTimer = 0, requests = 0, pendingEvidence;
  const pendingSearches = [], searchURLs = [], pendingReports=[],reportURLs=[];
  function element(id) {
    if (!elements.has(id)) elements.set(id, {
      value: '', textContent: '', hidden: false, disabled: false, open: false,isConnected:true,
      addEventListener(type, fn) { handlers.set(`${id}:${type}`, fn); },
      children: [], replaceChildren(...nodes) { this.value='';for(const child of this.children)child.isConnected=false;this.children=nodes;for(const child of nodes)child.isConnected=this.isConnected; }, append(...nodes) { this.children.push(...nodes);for(const child of nodes)child.isConnected=this.isConnected; }, contains() { return false; },
      showModal() { this.open = true; },
      close() { this.open = false; handlers.get(`${id}:close`)?.(); },
    });
    return elements.get(id);
  }
  const schedule = (fn, delay) => { const id = ++nextTimer; timers.set(id, {fn, delay}); return id; };
  const window = {
    addEventListener(type, fn) { handlers.set(`window:${type}`, fn); },
    dispatchEvent(event) { handlers.get(`window:${event.type}`)?.(event); },
    setTimeout: schedule, clearTimeout: id => timers.delete(id),
  };
  const document = {
    hidden: false, getElementById: element,
    addEventListener(type, fn) { handlers.set(`document:${type}`, fn); },
    createElement: tag => ({tag, textContent:'', children:[], dataset:{}, className:'', disabled:false,isConnected:false,
      addEventListener(type,fn){handlers.set(`${tag}:${type}`,fn);if(type==='click')this.onclick=fn}, append(...nodes){this.children.push(...nodes);for(const child of nodes)child.isConnected=this.isConnected},
      replaceChildren(...nodes){for(const child of this.children)child.isConnected=false;this.children=nodes;for(const child of nodes)child.isConnected=this.isConnected},remove(){this.isConnected=false},querySelector(selector){return this.children.flatMap(n=>[n,...(n.children||[])]).find(n=>selector==='.'+n.className&&n.className)||null},setAttribute(){}}),
    createTextNode: text => ({textContent: text}),
  };
  const context = vm.createContext({window, document, AbortController, URLSearchParams,
    Event,
    setTimeout: schedule, clearTimeout: id => timers.delete(id),
    Option: function(text, value) { this.text = text; this.value = value; },
    async fetch(url) {
      requests++;
      if (url.startsWith('/api/v1/search?')) {
        searchURLs.push(url);
        return new Promise(resolve => pendingSearches.push(data => resolve({ok: true, json: async () => data})));
      }
      if(url.includes('/api/v1/sources/')&&url.includes('/report?')){reportURLs.push(url);return new Promise(resolve=>pendingReports.push(data=>resolve({ok:true,json:async()=>data})));}
      if (url.startsWith('/api/v1/documents/')) return new Promise(resolve => {
        pendingEvidence = data => resolve({ok: true, json: async () => data});
      });
      if(url.endsWith('/capabilities'))return {ok:true,json:async()=>({management:true})};
      if(url.endsWith('/session'))return {ok:true,json:async()=>({token:'synthetic-capability'})};
      return {ok: true, json: async () => url.includes('/sync')
        ? {enabled: true, sources: []} : {sources: sourcePayload}};
    },
  });
  vm.runInContext(script, context);
  return {context, timers, element, document, handlers,
    notify: (type, detail) => handlers.get(`window:${type}`)?.({detail}),
    get requests() { return requests; },
    resolveEvidence: data => pendingEvidence(data),
    resolveSearch: (index, data) => pendingSearches[index](data),resolveReport:(index,data)=>pendingReports[index](data), searchURLs,reportURLs};
}

test('inventory notifications retain one poll and pause in a hidden tab', async () => {
  const h = harness(); await flush();
  assert.equal(h.timers.size, 1);
  for (let i = 0; i < 5; i++) { h.notify('sources-changed'); await flush(); }
  assert.equal(h.timers.size, 1, 'notifications must not spawn additional polling loops');
  h.document.hidden = true; h.handlers.get('document:visibilitychange')?.();
  assert.equal(h.timers.size, 0, 'hidden tabs must stop periodic polling');
  h.document.hidden = false; h.handlers.get('document:visibilitychange')?.(); await flush();
  assert.equal(h.timers.size, 1);
});

test('overlapping inventory notifications are coalesced', async () => {
  const h = harness(); await flush();
  const before = h.requests;
  for (let i = 0; i < 10; i++) h.notify('sources-changed');
  await flush();
  assert.ok(h.requests - before <= 4, 'one active refresh and at most one follow-up');
  assert.equal(h.timers.size, 1);
});

test('source removal invalidates evidence that was fetched before removal', async () => {
  const h = harness(); await flush();
  const preview = vm.runInContext("preview('doc', 0, 'removed-source')", h.context);
  await flush();
  assert.equal(h.element('preview').open, true);
  h.notify('source-removed', 'removed-source');
  h.resolveEvidence({id: 'doc', source_id: 'removed-source', title: 'Removed',
    source_name: 'Old source', path: 'note.md', text: 'stale text',
    source_kind: 'filesystem', modified_at: '2026-01-01T00:00:00Z', uri: 'file:///tmp/note.md'});
  await preview;
  assert.equal(h.element('preview').open, false);
  assert.equal(h.element('copy-markdown').disabled, true);
  assert.equal(vm.runInContext('currentEvidence', h.context), null);
});

test('archive source card identifies frozen provenance and offers no refresh/configure action', async () => {
  const hostile = '<img src=x onerror=alert(1)>';
  const h = harness([{id:'archive-1',kind:'archive',name:hostile,documents:1,last_indexed_at:'2026-10-09T12:00:00Z',
    archive:{origin:{kind:'filesystem',location:hostile},fingerprint:'abc',
      original_indexed_at:'2026-10-09T11:00:00Z',imported_at:'2026-10-09T12:00:00Z'}}]);
  await vm.runInContext(`(async()=>{${managementScript};globalThis.__loadSources=loadSources})()`,h.context); await flush(); await flush();
  const cards=h.element('source-list').children;
  assert.equal(cards.length,1);
  const flat=[];const walk=n=>{flat.push(n);for(const c of n.children||[])walk(c)};walk(cards[0]);
  assert.ok(flat.some(n=>n.textContent==='Archive snapshot'));
  assert.ok(flat.some(n=>n.textContent===hostile),'hostile name must remain literal text');
  assert.ok(flat.some(n=>n.textContent.includes('Historical original location: '+hostile)));
  const labels=flat.filter(n=>n.tag==='button').map(n=>n.textContent);
  assert.ok(labels.includes('Search this source')&&labels.includes('Remove from index'));
  assert.ok(!labels.includes('Refresh')&&!labels.includes('Configure'));
  const reportButton=flat.find(n=>n.tag==='button'&&n.textContent==='Indexing report');reportButton.onclick();await flush();
  h.resolveReport(0,{available:false,availability:'frozen_origin_report_not_in_portable_v1',source_id:'archive-1',source_kind:'archive'});await flush();
  const after=[];const walkAfter=n=>{after.push(n);for(const child of n.children||[])walkAfter(child)};walkAfter(cards[0]);
  assert.ok(after.some(n=>n.textContent.includes('Origin indexing diagnostics unavailable')));
  assert.equal(h.reportURLs.length,1,'archive diagnostics are read only and do not trigger scans');
});

test('report panel opts into paths and ignores a late response after source removal',async()=>{
  const sources=[{id:'fs-one',kind:'filesystem',name:'Notes',root:'/local/private',documents:1,last_indexed_at:'2026-10-10T00:00:00Z',max_docx_bytes:0,indexing_report:{available:true,report_id:'r1',finished_at:'2026-10-10T00:00:00Z',indexed_documents:1,skipped_files:1,pruned_directories:0}}];
  const h=harness(sources);await vm.runInContext(`(async()=>{${managementScript};globalThis.__loadSources=loadSources})()`,h.context);await flush();await flush();
  const card=h.element('source-list').children[0],walk=n=>[n,...(n.children||[]).flatMap(walk)];const button=walk(card).find(n=>n.tag==='button'&&n.textContent==='Indexing report');assert.ok(button);button.onclick();await flush();
  assert.ok(h.reportURLs[0].endsWith('include_paths=true'));
  sources.splice(0,1);await h.context.__loadSources();await flush();sources.push({id:'fs-one',kind:'filesystem',name:'Notes re-added',root:'/local/private',documents:1,last_indexed_at:'2026-10-10T00:01:00Z',max_docx_bytes:0,indexing_report:{available:true,report_id:'r2',finished_at:'2026-10-10T00:01:00Z',indexed_documents:1,skipped_files:0,pruned_directories:0}});await h.context.__loadSources();await flush();
  h.resolveReport(0,{available:true,availability:'available',report:{id:'stale',operation:'refresh',indexed_documents:1,skipped_files:1,pruned_directories:0,reasons:[],examples:[{path:'private/stale.txt',reason:'unsupported_format'}]}});await flush();
  assert.equal(card.isConnected,false);assert.equal(h.element('source-list').children.length,1);assert.equal(h.element('source-list').children[0].querySelector('.indexing-report'),null);
});

test('report details render coverage, next steps, and hostile relative paths as text',async()=>{
  const hostile='<img src=x onerror=alert(1)>.png',source={id:'fs-detail',kind:'filesystem',name:'Notes',root:'/private',documents:1,last_indexed_at:'2026-10-10T00:00:00Z',max_text_bytes:1048576,max_pdf_bytes:16777216,max_docx_bytes:0,indexing_report:{available:true,report_id:'r',finished_at:'2026-10-10T00:00:00Z',indexed_documents:1,skipped_files:1,pruned_directories:0}};
  const h=harness([source]);await vm.runInContext(`(async()=>{${managementScript};globalThis.__loadSources=loadSources})()`,h.context);await flush();await flush();
  const card=h.element('source-list').children[0],walk=n=>[n,...(n.children||[]).flatMap(walk)],button=walk(card).find(n=>n.tag==='button'&&n.textContent==='Indexing report');button.onclick();await flush();
  h.resolveReport(0,{available:true,availability:'available',report:{id:'r',operation:'refresh',finished_at:'2026-10-10T00:00:00Z',indexed_documents:1,skipped_files:1,pruned_directories:0,coverage:'complete_filesystem',observed_files:2,observed_files_known:true,observed_entries:3,observed_entries_known:true,observed_directories:1,observed_directories_known:true,reasons:[{code:'format_disabled',unit:'file',count:1}],examples:[{path:hostile,unit:'file',reason:'unsupported_format'}],examples_omitted:0,redacted_samples:0}});await flush();
  const panel=card.children.find(n=>n.className==='indexing-report'),rendered=walk(panel);assert.ok(rendered.some(n=>n.textContent.includes('complete_filesystem')));assert.ok(rendered.some(n=>n.textContent.includes('Configure action')));assert.ok(rendered.some(n=>n.textContent===hostile+' · unsupported_format'));assert.equal(rendered.filter(n=>n.tag==='img').length,0);
});

test('job UI shows partial counters separately from the committed source report',async()=>{
  const h=harness([{id:'fs-job',kind:'filesystem',name:'Notes',root:'/private',documents:3,last_indexed_at:'2026-10-10T00:00:00Z',max_docx_bytes:0,indexing_report:{available:true,report_id:'durable-1',finished_at:'2026-10-10T00:00:00Z',indexed_documents:3,skipped_files:2,pruned_directories:1}}]);
  let job={id:'attempt-1',type:'refresh',source_id:'fs-job',status:'running',phase:'enumerating_extracting',progress:{attempt_id:'attempt-1',sequence:3,phase:'enumerating_extracting',processed_documents:2,skipped_entries:1,elapsed_millis:40,partial:true,committed:false}};
  h.context.fetch=async url=>url.endsWith('/jobs')?{ok:true,json:async()=>({jobs:[job]})}:url.endsWith('/sync')?{ok:true,json:async()=>({enabled:true,sources:[]} )}:{ok:true,json:async()=>({sources:[{id:'fs-job',kind:'filesystem',name:'Notes',root:'/private',documents:3,last_indexed_at:'2026-10-10T00:00:00Z',max_docx_bytes:0,indexing_report:{available:true,report_id:'durable-1',finished_at:'2026-10-10T00:00:00Z',indexed_documents:3,skipped_files:2,pruned_directories:1}}]})};
  await vm.runInContext(`(async()=>{${managementScript};globalThis.__pollDiagnosticsJob=pollJobs})()`,h.context);await h.context.__pollDiagnosticsJob();
  assert.ok(h.element('job-list').children.some(row=>/2 documents processed · 1 entries skipped/.test(row.textContent)));
  assert.ok(h.element('source-list').children.length===1);
  job={...job,status:'failed',phase:'done',error_code:'scan_failed',error:'Scan did not commit; previous report remains.',progress:{...job.progress,sequence:5,phase:'done',partial:true,committed:false,failure_code:'scan_failed'}};
  await h.context.__pollDiagnosticsJob();
  assert.match(h.element('management-status').textContent,/scan_failed.*2 processed, 1 entries skipped · not committed/);
  const sourceCard=h.element('source-list').children[0];assert.ok(sourceCard.children.some(node=>node.textContent.includes('3 indexed · 2 skipped · 1 pruned directories')),'committed source summary remains separately visible');
});

test('pending archive preview is invalidated when the archive source is removed', async () => {
  const h=harness();await flush();
  const preview=vm.runInContext("preview('archive-doc', 0, 'archive-source')",h.context);await flush();
  h.notify('source-removed','archive-source');
  h.resolveEvidence({id:'archive-doc',source_id:'archive-source',title:'snapshot',source_name:'archive',
    path:'note.md',text:'stale archived text',source_kind:'archive',modified_at:'2026-10-09T00:00:00Z',uri:'file:///old/note.md'});
  await preview;assert.equal(h.element('preview').open,false);assert.equal(vm.runInContext('currentEvidence',h.context),null);
});

test('successful source reconfiguration invalidates pending preview evidence', async () => {
  const h = harness(); await flush();
  const preview = vm.runInContext("preview('doc', 0, 'configured-source')", h.context);
  await flush();
  h.notify('source-reconfigured', 'configured-source');
  h.resolveEvidence({id: 'doc', source_id: 'configured-source', title: 'Old snapshot',
    source_name: 'Folder', path: 'note.docx', text: 'stale DOCX snapshot',
    source_kind: 'filesystem', modified_at: '2026-01-01T00:00:00Z', uri: 'file:///tmp/note.docx'});
  await preview;
  assert.equal(h.element('preview').open, false);
  assert.equal(h.element('copy-markdown').disabled, true);
  assert.equal(vm.runInContext('currentEvidence', h.context), null);
});

test('job polling resumes if the tab is hidden during an active request', async () => {
  const h = harness(); await flush(); h.timers.clear();
  let releaseJobs;
  h.context.fetch = async url => {
    if (url.endsWith('/capabilities')) return {ok: true, json: async () => ({management: false})};
    if (url.endsWith('/jobs') && !releaseJobs) return new Promise(resolve => {
      releaseJobs = () => resolve({ok: true, json: async () => ({jobs: []})});
    });
    return {ok: true, json: async () => url.endsWith('/jobs') ? {jobs: []}
      : url.endsWith('/sync') ? {enabled: true, sources: []} : {sources: []}};
  };
  const management = [...html.matchAll(/<script type="module">([\s\S]*?)<\/script>/g)][1][1];
  await vm.runInContext(`(async () => {${management}; globalThis.runJobPoll = pollJobs;})()`, h.context);
  const poll = h.context.runJobPoll(); await flush();
  h.document.hidden = true; h.handlers.get('document:visibilitychange')();
  releaseJobs(); await poll;
  assert.equal(h.timers.size, 0, 'an in-flight poll must not leave a hidden-tab timer');
  h.document.hidden = false; h.handlers.get('document:visibilitychange')();
  assert.equal(h.timers.size, 1, 'showing the tab must restart job polling');
});

test('newer search response wins when an older fetch completes late', async () => {
  const h=harness();await flush();h.element('query').value='retry';
  const submit=()=>h.handlers.get('search-form:submit')({preventDefault(){}});
  submit();await flush();submit();await flush();
  h.resolveSearch(1,{total:2,results:[]});await flush();
  h.resolveSearch(0,{total:99,results:[]});await flush();
  assert.match(h.element('status').textContent,/2 matching documents/);
});

test('filter reset issues a new server request from a coherent form snapshot', async () => {
  const h=harness();await flush();h.element('query').value='retry';h.element('mode').value='advanced';
  h.element('format').value='pdf';h.element('path-prefix').value='docs';h.element('title-contains').value='runbook';
  h.handlers.get('search-form:submit')({preventDefault(){}});await flush();
  const first=new URLSearchParams(h.searchURLs[0].split('?')[1]);
  assert.equal(first.get('mode'),'advanced');assert.equal(first.get('format'),'pdf');
  assert.equal(first.get('path_prefix'),'docs');assert.equal(first.get('title_contains'),'runbook');
  h.resolveSearch(0,{total:0,results:[]});await flush();
  h.handlers.get('reset-filters:click')();await flush();
  const reset=new URLSearchParams(h.searchURLs[1].split('?')[1]);
  assert.equal(reset.get('format'),'all');assert.equal(reset.has('path_prefix'),false);
  assert.equal(reset.has('title_contains'),false);assert.equal(h.element('query').value,'retry');
  assert.equal(h.element('active-filters').textContent,'');
});
