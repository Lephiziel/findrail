import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import vm from 'node:vm';
import { sourceFreshness, sourceHealth } from './freshness.mjs';

const html = readFileSync(new URL('./index.html', import.meta.url), 'utf8');
const lifecycle = readFileSync(new URL('./search_ui.mjs', import.meta.url), 'utf8');
const presetScript = lifecycle.slice(0,lifecycle.indexOf('const initializedDocuments')).replace(/^import .*;$/gm,'').replace(/^export /gm,'');
const lifecycleOpen = lifecycle.indexOf('{\n', lifecycle.indexOf('export function initializeSearchUI(')) + 2;
const script = lifecycle.slice(lifecycleOpen, lifecycle.lastIndexOf('\n}'))
  .replace(/^if \(!document \|\| initializedDocuments\.has\(document\)\) return;\ninitializedDocuments\.add\(document\);\n/m, '')
  .replace(/^import .*;$/gm, '');
const previewModule = readFileSync(new URL('./preview_ui.mjs', import.meta.url), 'utf8');
const previewFunctionStart = previewModule.indexOf('export function createPreviewLifecycle(');
const previewFunctionEnd = previewModule.indexOf('\n}\n\nexport async function initializePreviewUI', previewFunctionStart) + 2;
const previewScript = previewModule.slice(previewFunctionStart, previewFunctionEnd).replace(/^export /, '');
const managementScript = [...html.matchAll(/<script type="module">([\s\S]*?)<\/script>/g)][1][1]
  .replace(/^import .*;$/gm, '');
const flush = () => new Promise(resolve => setImmediate(resolve));

function harness(sourcePayload = [], capabilities = {management:true}) {
  const elements = new Map(), handlers = new Map(), timers = new Map(), windowEvents = [];
  let currentSources = sourcePayload;
  const frames = [];
  let nextTimer = 0, requests = 0, pendingEvidence;
  const pendingSearches = [], searchURLs = [], pendingReports=[],reportURLs=[],documentURLs=[];
  const descendants = root => (root.children || []).flatMap(child => [child, ...descendants(child)]);
  const matches = (node, selector) => selector.startsWith('.') ? node.className?.split(' ').includes(selector.slice(1)) : node.tagName === selector.toUpperCase();
  function makeNode(tag, id = '') {
    return {id, tag, tagName:tag.toUpperCase(), value:'', textContent:'', hidden:false, disabled:false, open:false, isConnected:!!id, children:[], dataset:{}, className:'',
      addEventListener(type,fn){handlers.set(`${id || tag}:${type}`,fn);if(type==='click')this.onclick=fn},
      contains(node){return this===node||descendants(this).includes(node)},
      closest(selector){for(let node=this;node;node=node.parentElement)if(matches(node,selector))return node;return null},
      querySelectorAll(selector){return descendants(this).filter(node=>matches(node,selector))},
      querySelector(selector){return this.querySelectorAll(selector)[0]||null},
      focus(){if(this.isConnected&&!this.disabled)document.activeElement=this},
      append(...nodes){this.children.push(...nodes);for(const node of nodes){node.parentElement=this;for(const child of [node,...descendants(node)])child.isConnected=this.isConnected}},
      replaceChildren(...nodes){if(this.contains(document.activeElement))document.activeElement=document.body;for(const node of descendants(this))node.isConnected=false;this.children=[];this.value='';this.append(...nodes)},
      remove(){for(const node of [this,...descendants(this)])node.isConnected=false},
      setAttribute(name,value){this[name]=value},
      showModal(){this.returnFocus=document.activeElement;this.open=true;this.focus()},
      close(){this.open=false;this.returnFocus?.focus();handlers.get(`${id}:close`)?.()},
    };
  }
  function element(id) {
    if (!elements.has(id)) elements.set(id, makeNode(['preview','evidence-collection','diagnostics-dialog','configure-source','remove-confirm'].includes(id)?'dialog':'div', id));
    return elements.get(id);
  }
  const schedule = (fn, delay) => { const id = ++nextTimer; timers.set(id, {fn, delay}); return id; };
  const window = {
    addEventListener(type, fn) { handlers.set(`window:${type}`, fn); },
    dispatchEvent(event) { windowEvents.push(event); handlers.get(`window:${event.type}`)?.(event); },
    setTimeout: schedule, clearTimeout: id => timers.delete(id),
  };
  const document = {
    hidden: false, getElementById: element,
    addEventListener(type, fn) { handlers.set(`document:${type}`, fn); },
    createElement: tag => makeNode(tag),
    createTextNode: text => ({textContent: text}),
    querySelectorAll: selector => [...elements.values()].filter(node=>matches(node,selector)),
  };
  document.body=element('body');document.activeElement=document.body;
  const context = vm.createContext({window, document, AbortController, URLSearchParams, TextEncoder, TextDecoder, sourceFreshness, sourceHealth,
    Event, CustomEvent:class extends Event{constructor(type,{detail}){super(type);this.detail=detail}}, requestAnimationFrame: fn => frames.push(fn),
    setTimeout: schedule, clearTimeout: id => timers.delete(id),
    Option: function(text, value) { this.text = text; this.value = value; },
    async fetch(url) {
      requests++;
      if (url.startsWith('/api/v1/search?') || url.startsWith('/api/v1/search/pages?')) {
        searchURLs.push(url);
        return new Promise(resolve => pendingSearches.push((data,status=200) => resolve({ok: status===200,status, json: async () => data})));
      }
      if(url.includes('/api/v1/sources/')&&url.includes('/report?')){reportURLs.push(url);return new Promise(resolve=>pendingReports.push(data=>resolve({ok:true,json:async()=>data})));}
      if (url.startsWith('/api/v1/documents/')) return new Promise(resolve => {
        documentURLs.push(url);pendingEvidence = (data,status=200) => resolve({ok: status===200,status, json: async () => data});
      });
      if(url.endsWith('/capabilities'))return {ok:true,json:async()=>capabilities};
      if(url.endsWith('/session'))return {ok:true,json:async()=>({token:'synthetic-capability'})};
      return {ok: true, json: async () => url.includes('/sync')
        ? {enabled: true, sources: []} : {sources: currentSources}};
    },
  });
  vm.runInContext(presetScript, context);
  vm.runInContext(previewScript, context);
  vm.runInContext(script, context);
  return {context, timers, element, document, handlers, windowEvents, setSources(value){currentSources=value},runFrames(){while(frames.length)frames.shift()()},
    notify: (type, detail) => handlers.get(`window:${type}`)?.({detail}),
    get requests() { return requests; },
    resolveEvidence: (data,status) => pendingEvidence(data,status),
     resolveSearch: (index, data, status) => pendingSearches[index](data,status),resolveReport:(index,data)=>pendingReports[index](data), searchURLs,reportURLs,documentURLs};
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

test('malformed source inventories are rejected before collection reconciliation', async () => {
  const h = harness([{id:'source-a',kind:'filesystem',name:'Synthetic',root:'/synthetic',documents:1}]);
  await flush();
  const broadcasts = h.windowEvents.filter(event => event.type === 'findrail-inventory').length;
  h.setSources([{id:{},kind:'filesystem',name:'Malformed',root:'/synthetic',documents:1}]);
  h.notify('sources-changed');
  await flush();
  assert.equal(h.windowEvents.filter(event => event.type === 'findrail-inventory').length, broadcasts);
  assert.match(h.element('health').textContent, /Malformed source inventory/);
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
  assert.equal(vm.runInContext('previewUI.currentEvidence', h.context), null);
});

test('archive source card identifies frozen provenance and offers no refresh/configure action', async () => {
  const hostile = '<img src=x onerror=alert(1)>';
  const h = harness([{id:'archive-1',kind:'archive',name:hostile,root:'',documents:1,last_indexed_at:'2026-10-09T12:00:00Z',
    archive:{origin:{kind:'filesystem',location:'/private/origin-should-stay-hidden'},fingerprint:'abc',
      original_indexed_at:'2026-10-09T11:00:00Z',imported_at:'2026-10-09T12:00:00Z'}}]);
  await vm.runInContext(`(async()=>{${managementScript};globalThis.__loadSources=loadSources})()`,h.context); await flush(); await flush();
  const cards=h.element('source-list').children;
  assert.equal(cards.length,1);
  const flat=[];const walk=n=>{flat.push(n);for(const c of n.children||[])walk(c)};walk(cards[0]);
  assert.ok(flat.some(n=>n.textContent==='Archive snapshot'));
  assert.ok(flat.some(n=>n.textContent.includes('Frozen indexed snapshot')));
  assert.ok(flat.some(n=>n.textContent===hostile),'hostile source name must remain literal text');
  assert.ok(!flat.some(n=>n.textContent.includes('/private/origin-should-stay-hidden')),'archive inventory must not disclose its private origin path');
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
  await preview;assert.equal(h.element('preview').open,false);assert.equal(vm.runInContext('previewUI.currentEvidence',h.context),null);
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
  assert.equal(vm.runInContext('previewUI.currentEvidence', h.context), null);
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
  h.context.window.findrailBootstrap = Promise.resolve({capabilities:{management:false,open_originals:false},token:''});
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

test('cursor paging uses response cursors and actual ranges; Previous and First reset safely', async () => {
  const h=harness([],{search_pagination:true});await flush();h.element('query').value='retry';
  h.handlers.get('search-form:submit')({preventDefault(){}});await flush();
  assert.match(h.searchURLs[0],/^\/api\/v1\/search\/pages\?/);
  assert.equal(new URLSearchParams(h.searchURLs[0].split('?')[1]).get('limit'),'20');
  h.resolveSearch(0,{total:37,offset:0,returned:20,results:[],next_cursor:'opaque/next',end_reason:null});await flush();
  assert.equal(h.element('page-range').textContent,'1–20 of 37');
  h.handlers.get('page-next:click')();await flush();
  const next=new URLSearchParams(h.searchURLs[1].split('?')[1]);
  assert.equal(next.get('cursor'),'opaque/next');
  h.resolveSearch(1,{total:37,offset:20,returned:17,results:[],next_cursor:null,end_reason:'complete'});await flush();
  assert.equal(h.element('page-range').textContent,'21–37 of 37');
  h.handlers.get('page-previous:click')();await flush();
  assert.equal(new URLSearchParams(h.searchURLs[2].split('?')[1]).has('cursor'),false);
  h.resolveSearch(2,{total:37,offset:0,returned:20,results:[],next_cursor:'opaque/next',end_reason:null});await flush();
  h.element('page-size').value='50';h.handlers.get('page-size:change')();await flush();
  assert.equal(new URLSearchParams(h.searchURLs[3].split('?')[1]).get('limit'),'50');
  assert.equal(new URLSearchParams(h.searchURLs[3].split('?')[1]).has('cursor'),false);
  h.resolveSearch(3,{total:37,offset:0,returned:37,results:[],next_cursor:null,end_reason:'complete'});await flush();
  h.handlers.get('page-first:click')();await flush();
  assert.equal(new URLSearchParams(h.searchURLs[4].split('?')[1]).has('cursor'),false);
  assert.equal(h.element('page-range').textContent,'1–37 of 37','the range remains the last successful response while First is pending');
});

test('a stale page cursor exposes explicit restart without automatically mixing snapshots',async()=>{
  const h=harness([],{search_pagination:true});await flush();h.element('query').value='retry';
  h.handlers.get('search-form:submit')({preventDefault(){}});await flush();
  h.resolveSearch(0,{total:40,offset:0,returned:20,results:[],next_cursor:'cursor-1',end_reason:null});await flush();
  h.handlers.get('page-next:click')();await flush();
  h.resolveSearch(1,{error:'stale cursor',code:'cursor_stale'},409);await flush();
  assert.match(h.element('status').textContent,/Data changed; restart/);
  assert.equal(h.searchURLs.length,2,'stale cursor does not trigger an automatic retry');
  h.handlers.get('page-first:click')();await flush();
  assert.equal(new URLSearchParams(h.searchURLs[2].split('?')[1]).has('cursor'),false);
  h.resolveSearch(2,{total:40,offset:0,returned:20,results:[],next_cursor:'fresh-cursor',end_reason:null});await flush();
  assert.equal(h.element('page-range').textContent,'1–20 of 40');
});

test('a delayed cursor response cannot replace results from a newer query generation',async()=>{
  const h=harness([],{search_pagination:true});await flush();h.element('query').value='old query';
  h.handlers.get('search-form:submit')({preventDefault(){}});await flush();
  h.resolveSearch(0,{total:40,offset:0,returned:20,results:[],next_cursor:'old-cursor',end_reason:null});await flush();
  h.handlers.get('page-next:click')();await flush();
  h.element('query').value='new query';h.handlers.get('search-form:submit')({preventDefault(){}});await flush();
  h.resolveSearch(2,{total:1,offset:0,returned:1,results:[],next_cursor:null,end_reason:'complete'});await flush();
  h.resolveSearch(1,{total:40,offset:20,returned:20,results:[],next_cursor:'older',end_reason:null});await flush();
  assert.equal(h.element('page-range').textContent,'1–1 of 1');
  assert.match(h.element('status').textContent,/1 matching documents/);
  assert.equal(new URLSearchParams(h.searchURLs[2].split('?')[1]).get('q'),'new query');
});

test('editing the query aborts a pending navigation before Search is submitted',async()=>{
  const h=harness([],{search_pagination:true});await flush();h.element('query').value='first';
  h.handlers.get('search-form:submit')({preventDefault(){}});await flush();
  h.resolveSearch(0,{total:40,offset:0,returned:20,results:[],next_cursor:'cursor-1',end_reason:null});await flush();
  h.handlers.get('page-next:click')();await flush();
  h.element('query').value='second';h.handlers.get('query:input')();
  h.resolveSearch(1,{total:40,offset:20,returned:20,results:[],next_cursor:'cursor-2',end_reason:null});await flush();
  assert.equal(h.element('search-pager').hidden,true);
  assert.match(h.element('status').textContent,/options changed/);
});

test('every search filter starts a fresh cursor chain with its current value',async()=>{
  const h=harness([],{search_pagination:true});await flush();h.element('query').value='retry';
  h.handlers.get('search-form:submit')({preventDefault(){}});await flush();
  h.resolveSearch(0,{total:30,offset:0,returned:20,results:[],next_cursor:'cursor-1',end_reason:null});await flush();
  for(const [id,value,param] of [['mode','advanced','mode'],['source','source-7','source'],['format','pdf','format'],['path-prefix','notes/','path_prefix'],['title-contains','Runbook','title_contains']]){
    h.element(id).value=value;h.handlers.get(`${id}:change`)();await flush();
    const params=new URLSearchParams(h.searchURLs.at(-1).split('?')[1]);
    assert.equal(params.get(param),value,`${id} filter should reach the page API`);
    assert.equal(params.has('cursor'),false,`${id} filter should restart from First`);
    h.resolveSearch(h.searchURLs.length-1,{total:0,offset:0,returned:0,results:[],next_cursor:null,end_reason:'complete'});await flush();
  }
});

async function pagingContractHarness() {
  const sources=[{id:'source-a',name:'Synthetic',root:'/synthetic',kind:'filesystem',documents:40,last_indexed_at:'2026-10-16T00:00:00Z'}];
  const h=harness(sources,{search_pagination:true});await flush();
  h.element('query').value='original';h.element('mode').value='literal';h.element('format').value='all';h.element('page-size').value='20';
  h.handlers.get('search-form:submit')({preventDefault(){}});await flush();
  const row={id:'doc-old',source_id:'source-a',source_name:'Synthetic',source_kind:'filesystem',title:'Original',snippet:'original',uri:'file:///synthetic/old.md',media_type:'text/plain'};
  h.resolveSearch(0,{total:40,offset:0,returned:1,limit:20,window_limit:1000,results:[row],next_cursor:'cursor-1',end_reason:null});await flush();
  return h;
}

test('page size follows the current form while a filter search is pending',async()=>{
  const h=await pagingContractHarness();
  h.element('source').value='source-new';h.handlers.get('source:change')();await flush();
  h.element('query').value='edited';h.handlers.get('query:input')();
  h.element('page-size').value='50';h.handlers.get('page-size:change')();await flush();
  const params=new URLSearchParams(h.searchURLs.at(-1).split('?')[1]);
  assert.equal(params.get('q'),'edited');assert.equal(params.get('source'),'source-new');assert.equal(params.get('limit'),'50');
  assert.equal(params.has('cursor'),false);
});

test('inventory refresh cannot resurrect a search after the query was edited',async()=>{
  const h=await pagingContractHarness();
  h.element('query').value='edited';h.handlers.get('query:input')();
  h.setSources([{id:'source-a',name:'Synthetic',root:'/synthetic',kind:'filesystem',documents:41,last_indexed_at:'2026-10-16T00:01:00Z'}]);
  h.notify('sources-changed');await flush();
  assert.equal(h.searchURLs.length,1,'refresh submitted a query that no longer matches the form');
  assert.equal(h.element('search-pager').hidden,true);
});

test('failed navigation preserves the last displayed page and range for retry',async()=>{
  const h=await pagingContractHarness();
  h.handlers.get('page-next:click')();await flush();
  h.resolveSearch(1,{error:'Search timed out',code:'search_page_timeout'},504);await flush();
  assert.equal(h.element('results').querySelector('.result')?.dataset.documentId,'doc-old');
  assert.equal(h.element('page-range').textContent,'1–1 of 40');
  assert.match(h.element('status').textContent,/timed out/i);
  h.handlers.get('page-next:click')();await flush();
  assert.equal(new URLSearchParams(h.searchURLs.at(-1).split('?')[1]).get('cursor'),'cursor-1');
});

test('stale cursor returns pager focus to the enabled restart control',async()=>{
  const h=await pagingContractHarness();
  h.element('page-next').focus();h.handlers.get('page-next:click')();await flush();
  h.resolveSearch(1,{error:'Indexed data changed',code:'cursor_stale'},409);await flush();
  assert.equal(h.document.activeElement,h.element('page-first'));
  assert.equal(h.element('page-first').disabled,false);
});

test('navigation does not steal focus from a query edited during the request',async()=>{
  const h=await pagingContractHarness();
  h.element('page-next').focus();h.handlers.get('page-next:click')();await flush();
  h.element('query').focus();
  h.resolveSearch(1,{total:40,offset:1,returned:0,limit:20,window_limit:1000,results:[],next_cursor:null,end_reason:'complete'});await flush();
  assert.equal(h.document.activeElement,h.element('query'));
});

test('changing page size invalidates a pending preview from the outgoing result page',async()=>{
  const h=await pagingContractHarness();
  vm.runInContext("preview('doc-old',0,'source-a','text/plain',null,'old-identity',{query:'original',mode:'literal',source:'',format:'all',path_prefix:'',title_contains:''})",h.context);await flush();
  h.element('page-size').value='50';h.handlers.get('page-size:change')();await flush();
  h.resolveEvidence({...lateEvidence,id:'doc-old',original_identity:'old-identity'});await flush();
  assert.equal(h.element('preview').open,false);
  assert.equal(vm.runInContext('previewUI.currentEvidence',h.context),null);
});

test('automatic result refresh preserves an explicit saved-evidence preview request',async()=>{
  const h=await pagingContractHarness();
  h.context.window.findrailPreview('doc-old',0,'source-a','text/plain');await flush();
  vm.runInContext("search({query:'original',mode:'literal',source:'',format:'all',path_prefix:'',title_contains:''},true)",h.context);await flush();
  h.resolveEvidence({...lateEvidence,id:'doc-old'});await flush();
  assert.equal(h.element('preview').open,true);
  assert.equal(vm.runInContext('previewUI.currentEvidence?.id',h.context),'doc-old');
});

test('forgetting a selected source does not implicitly rerun Search or splice old results',async()=>{
  const h=await pagingContractHarness();
  h.element('source').value='source-a';h.handlers.get('source:change')();await flush();
  h.resolveSearch(1,{total:40,offset:0,returned:0,results:[],next_cursor:'old-source-cursor',end_reason:null});await flush();
  h.setSources([]);h.notify('sources-changed');await flush();
  assert.equal(h.element('search-pager').hidden,false);
  assert.equal(h.searchURLs.length,2);
  assert.match(h.element('status').textContent,/Existing results are unchanged/);
});

test('an initial page timeout exposes First and retries the current form without a cursor',async()=>{
  const h=harness([],{search_pagination:true});await flush();h.element('query').value='retry me';
  h.handlers.get('search-form:submit')({preventDefault(){}});await flush();
  h.resolveSearch(0,{error:'Timed out',code:'search_page_timeout'},504);await flush();
  assert.equal(h.element('page-first').disabled,false);
  h.handlers.get('page-first:click')();await flush();
  const params=new URLSearchParams(h.searchURLs.at(-1).split('?')[1]);
  assert.equal(params.get('q'),'retry me');assert.equal(params.has('cursor'),false);
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

const lateEvidence={id:'doc-a',source_id:'source-a',source_name:'Synthetic',source_kind:'filesystem',title:'Late note',path:'late.md',uri:'file:///synthetic/late.md',media_type:'text/plain',content_hash:'same-hash',original_identity:'snapshot-a',modified_at:'2026-10-15T00:00:00Z',page:0,page_count:0,text:'needle tail',text_start_char:80000,text_total_chars:80011,truncated:true,match_kind:'literal_body'};

async function focusedResultHarness() {
  const sources=[{id:'source-a',name:'Synthetic',root:'/synthetic',kind:'filesystem',documents:2,last_indexed_at:'2026-10-15T00:00:00Z'}];
  const h=harness(sources,{passage_preview:true});await flush();
  h.element('query').value='needle';h.element('mode').value='literal';h.element('format').value='all';
  h.handlers.get('search-form:submit')({preventDefault(){}});await flush();
  const first={...lateEvidence,snippet:'needle first'},second={...lateEvidence,id:'doc-b',title:'Another note',snippet:'needle second'};
  h.resolveSearch(0,{total:2,results:[first,second]});await flush();
  const find=id=>h.element('results').querySelectorAll('.result-preview').find(button=>button.dataset.documentId===id);
  find(first.id).focus();
  const refresh=async()=>{sources[0].last_indexed_at='2026-10-15T00:01:00Z';h.notify('sources-changed');await flush()};
  return {h,find,first,second,refresh};
}

test('inventory-only refresh preserves result identity and does not steal focus or rerun Search',async()=>{
  const {h,find,first,second,refresh}=await focusedResultHarness(),old=find(first.id);
  await refresh();assert.equal(h.document.activeElement,old);
  assert.equal(find(first.id),old);assert.equal(h.searchURLs.length,1);
});

test('inventory removal retains displayed result snapshot until explicit Search',async()=>{
  const {h,first,second,refresh}=await focusedResultHarness();await refresh();
  assert.equal(h.searchURLs.length,1);
  assert.equal(h.element('results').querySelectorAll('.result-preview').some(button=>button.dataset.documentId===first.id),true);
});

test('inventory refresh does not initiate a delayed search while the query is being edited',async()=>{
  const {h,first,second,refresh}=await focusedResultHarness();await refresh();h.element('query').focus();
  assert.equal(h.document.activeElement,h.element('query'));
  assert.equal(h.searchURLs.length,1);
});

test('Preview close resolves its return control in the animation frame and does not steal another dialog focus',async()=>{
  const {h,find,first,second}=await focusedResultHarness();find(first.id).onclick();await flush();
  h.resolveEvidence(lateEvidence);await flush();h.element('preview').close();
  vm.runInContext("search({query:'needle',mode:'literal',source:'',format:'all',path_prefix:'',title_contains:''},true)",h.context);await flush();
  h.resolveSearch(1,{total:2,results:[second,first]});await flush();h.runFrames();
  assert.equal(h.document.activeElement,find(first.id));
  find(first.id).onclick();await flush();h.resolveEvidence(lateEvidence);await flush();h.element('preview').close();
  h.element('diagnostics-dialog').showModal();h.runFrames();
  assert.equal(h.document.activeElement,h.element('diagnostics-dialog'));
});

test('late preview and manual navigation keep the result identity and code-point bounds',async()=>{
 const h=harness([{id:'source-a',name:'Synthetic',documents:1}],{passage_preview:true});await flush();
 vm.runInContext("preview('doc-a',0,'source-a','text/plain',null,'snapshot-a',{query:'needle',mode:'literal'})",h.context);await flush();
 assert.equal(new URL(h.documentURLs[0],'http://localhost').searchParams.get('original_identity'),'snapshot-a');
 assert.equal(new URL(h.documentURLs[0],'http://localhost').searchParams.get('q'),'needle');
 h.resolveEvidence(lateEvidence);await flush();
 assert.equal(h.element('previous-text').disabled,false);
 assert.equal(h.element('next-text').disabled,true,'a truncated tail has no next window');
 h.handlers.get('previous-text:click')();await flush();
 const previous=new URL(h.documentURLs[1],'http://localhost');
 assert.equal(previous.searchParams.get('start_char'),'14464');
 assert.equal(previous.searchParams.get('original_identity'),'snapshot-a');
 assert.equal(previous.searchParams.has('q'),false);
});

test('changed passage identity fails closed without falling back to a newer prefix',async()=>{
 const h=harness([{id:'source-a',name:'Synthetic',documents:1}],{passage_preview:true});await flush();
 vm.runInContext("preview('doc-a',0,'source-a','text/plain',null,'snapshot-a',{query:'needle',mode:'literal'})",h.context);await flush();
 h.resolveEvidence({error:'indexed snapshot changed'},409);await flush();
 assert.equal(h.documentURLs.length,1);
 assert.equal(h.element('add-evidence').disabled,true);
 assert.match(h.element('preview-body').textContent,/snapshot changed/);
});

test('changing query invalidates a delayed passage response',async()=>{
 const h=harness([{id:'source-a',name:'Synthetic',documents:1}],{passage_preview:true});await flush();
 vm.runInContext("preview('doc-a',0,'source-a','text/plain',null,'snapshot-a',{query:'needle',mode:'literal',source:'',format:'all',path_prefix:'',title_contains:''})",h.context);await flush();
 vm.runInContext("search({query:'different',mode:'literal',source:'',format:'all',path_prefix:'',title_contains:''})",h.context);
 h.resolveEvidence(lateEvidence);await flush();
 assert.equal(h.element('preview').open,false);
 assert.equal(h.element('add-evidence').disabled,true);
 assert.equal(vm.runInContext('previewUI.currentEvidence',h.context),null);
});

test('collection current-evidence bridge keeps PDF pages and saved text windows',async()=>{
 const h=harness([{id:'source-a',name:'Synthetic',documents:1}],{passage_preview:true});await flush();
 h.context.window.findrailPreview('doc-a',2,'source-a','application/pdf');await flush();
 assert.equal(h.documentURLs[0],'/api/v1/documents/doc-a?page=2');
 h.resolveEvidence({...lateEvidence,media_type:'application/pdf',page:2,page_count:2,text:'PDF page two'});await flush();
 assert.equal(h.element('page-label').textContent,'Page 2 / 2');
 h.element('preview').close();
 h.context.window.findrailPreview('doc-a',0,'source-a','text/plain',80000);await flush();
 h.resolveEvidence({...lateEvidence,text:'prefix',text_start_char:0});await flush();
 const manual=new URL(h.documentURLs.at(-1),'http://localhost');
 assert.equal(manual.searchParams.get('start_char'),'80000');
 assert.equal(manual.searchParams.get('original_identity'),'snapshot-a');
 assert.equal(manual.searchParams.has('q'),false);
 h.resolveEvidence(lateEvidence);await flush();
 assert.equal(h.element('text-range').textContent,'80000–80011 / 80011');
});

test('opening a changed collection document starts from its current beginning',async()=>{
 const h=harness([{id:'source-a',name:'Synthetic',documents:1}],{passage_preview:true});await flush();
 h.context.window.findrailPreview('doc-a',0,'source-a','text/plain',80000,'old-hash');await flush();
 h.resolveEvidence({...lateEvidence,text:'Current beginning',text_start_char:0});await flush();
 const manual=new URL(h.documentURLs.at(-1),'http://localhost');
 assert.equal(manual.searchParams.get('start_char'),'0');
 h.resolveEvidence({...lateEvidence,text:'Current beginning',text_start_char:0});await flush();
 assert.match(h.element('preview-notice').textContent,/Document changed; showing the beginning/);
});


const presetFixture=(query='synthetic query',scope='all')=>({format:'findrail-search-preset',version:1,title:'Synthetic preset',query,mode:'literal',filters:{format:'all',path_prefix:'',title_contains:''},source:{scope,id_hint:scope==='source'?'a':'',display_name:scope==='source'?'Synthetic':''}});
function deferredPresetFile(h){let resolve;const pending=new Promise(done=>{resolve=done});h.element('preset-file').files=[{size:1024,arrayBuffer:()=>pending}];const complete=h.handlers.get('preset-file:change')();return {complete,resolve(value){resolve(new TextEncoder().encode(JSON.stringify(value)).buffer)}}}
async function loadPreset(h,value){const file=deferredPresetFile(h);file.resolve(value);await file.complete;}

test('a newer preset file wins over an earlier delayed file read',async()=>{
 const h=harness();await flush();const first=deferredPresetFile(h),second=deferredPresetFile(h);
 second.resolve(presetFixture('newest query'));await second.complete;
 first.resolve(presetFixture('obsolete query'));await first.complete;
 h.handlers.get('preset-apply:click')();
 assert.equal(h.element('query').value,'newest query');
 assert.equal(h.searchURLs.length,0);
});

test('cancel invalidates a pending preset file read and returns focus',async()=>{
 const h=harness();await flush();h.element('query').value='current controls';const file=deferredPresetFile(h);
 h.handlers.get('preset-cancel:click')();file.resolve(presetFixture('canceled query'));await file.complete;
 assert.equal(h.element('preset-preview').hidden,true);
 assert.equal(h.element('preset-apply').disabled,true);
 assert.equal(h.element('query').value,'current controls');
 assert.equal(h.document.activeElement,h.element('preset-file'));
});

test('selecting a new file immediately disables applying the previous preset',async()=>{
 const h=harness();await flush();await loadPreset(h,presetFixture());const file=deferredPresetFile(h);
 assert.equal(h.element('preset-apply').disabled,true);
 file.resolve(presetFixture('replacement'));await file.complete;
});

test('source inventory changes refresh preset destinations and require a new explicit choice',async()=>{
 const sources=[{id:'a',name:'Synthetic',kind:'filesystem',root:'/synthetic',documents:1}];
 const h=harness(sources);await flush();await loadPreset(h,presetFixture('scoped','source'));
 h.element('preset-source-map').value='a';h.handlers.get('preset-source-map:change')();assert.equal(h.element('preset-apply').disabled,false);
 h.notify('findrail-inventory',[{id:'b',name:'New source',kind:'filesystem',root:'/new',documents:1}]);
 assert.equal(h.element('preset-source-map').value,'');
 assert.equal(h.element('preset-apply').disabled,true);
 assert.deepEqual(h.element('preset-source-map').children.map(option=>option.value),['','b']);
});

test('applying the same query invalidates its pending result preview',async()=>{
 const h=harness([{id:'a',name:'Synthetic',kind:'filesystem',root:'/synthetic',documents:1}]);await flush();
 h.element('query').value='same query';h.element('mode').value='literal';h.element('source').value='';h.element('format').value='all';
 const preview=vm.runInContext("preview('doc',0,'a','text/plain',null,'',snapshot())",h.context);await flush();
 await loadPreset(h,presetFixture('same query'));h.handlers.get('preset-apply:click')();
 h.resolveEvidence({id:'doc',source_id:'a',title:'Late evidence',source_name:'Synthetic',source_kind:'filesystem',media_type:'text/plain',path:'note.md',text:'obsolete preview',modified_at:'2026-01-01T00:00:00Z',uri:'file:///synthetic/note.md'});
 await preview;
 assert.equal(h.element('preview').open,false);
 assert.equal(vm.runInContext('previewUI.currentEvidence',h.context),null);
 assert.equal(h.searchURLs.length,0);
});

test('Escape from preset mapping cancels the load and returns to its file input',async()=>{
 const h=harness([{id:'a',name:'Synthetic',kind:'filesystem',root:'/synthetic',documents:1}]);await flush();await loadPreset(h,presetFixture('scoped','source'));
 h.handlers.get('preset-preview:keydown')?.({key:'Escape',preventDefault(){},stopPropagation(){}});
 assert.equal(h.element('preset-preview').hidden,true);
 assert.equal(h.document.activeElement,h.element('preset-file'));
});

test('preset apply clears a pending cursor response and only explicit Search starts the next request',async()=>{
 const h=harness([],{search_pagination:true});await flush();h.element('query').value='old query';
 h.handlers.get('search-form:submit')({preventDefault(){}});await flush();
 h.resolveSearch(0,{total:40,offset:0,returned:20,results:[],next_cursor:'old-cursor',end_reason:null});await flush();
 h.handlers.get('page-next:click')();await flush();
 await loadPreset(h,presetFixture('new query'));h.handlers.get('preset-apply:click')();
 h.resolveSearch(1,{total:40,offset:20,returned:20,results:[],next_cursor:'stale-next',end_reason:null});await flush();
 assert.equal(h.element('search-pager').hidden,true);assert.equal(h.element('results').children.length,0);assert.equal(h.searchURLs.length,2);
 assert.match(h.element('status').textContent,/Preset applied/);
 h.handlers.get('search-form:submit')({preventDefault(){}});await flush();
 const request=new URLSearchParams(h.searchURLs[2].split('?')[1]);assert.equal(request.get('q'),'new query');assert.equal(request.has('cursor'),false);
});

test('source removal and re-add cannot reuse an earlier preset mapping',async()=>{
 const current={id:'a',name:'Synthetic',kind:'filesystem',root:'/synthetic',documents:1};const h=harness([current]);await flush();
 await loadPreset(h,presetFixture('scoped','source'));h.element('preset-source-map').value='a';h.handlers.get('preset-source-map:change')();
 h.notify('source-removed','a');h.notify('findrail-inventory',[current]);
 assert.equal(h.element('preset-source-map').value,'');assert.equal(h.element('preset-apply').disabled,true);assert.equal(h.searchURLs.length,0);
});

test('pagehide invalidates a pending preset file read and clears its preview',async()=>{
 const h=harness();await flush();const file=deferredPresetFile(h);h.notify('pagehide');file.resolve(presetFixture('after pagehide'));await file.complete;
 assert.equal(h.element('preset-preview').hidden,true);assert.equal(h.element('preset-summary').textContent,'');assert.equal(h.element('preset-apply').disabled,true);
});

test('line breaks that single-line controls cannot preserve reject Apply without altering current settings',async()=>{
 for(const field of ['query','path_prefix','title_contains']){
  const h=harness();await flush();h.element('query').value='retained query';h.element('format').value='pdf';
  const preset=presetFixture();if(field==='query')preset.query='first\nsecond';else preset.filters[field]='first\rsecond';
  await loadPreset(h,preset);h.handlers.get('preset-apply:click')();
  assert.equal(h.element('query').value,'retained query');assert.equal(h.element('format').value,'pdf');assert.equal(h.searchURLs.length,0);
  assert.match(h.element('preset-status').textContent,/single.line|line breaks/);
 }
});
