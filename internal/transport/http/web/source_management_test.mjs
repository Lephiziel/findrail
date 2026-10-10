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
  const pendingSearches = [], searchURLs = [];
  function element(id) {
    if (!elements.has(id)) elements.set(id, {
      value: '', textContent: '', hidden: false, disabled: false, open: false,
      addEventListener(type, fn) { handlers.set(`${id}:${type}`, fn); },
      children: [], replaceChildren() { this.value = ''; this.children = []; }, append(...nodes) { this.children.push(...nodes); }, contains() { return false; },
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
    createElement: tag => ({tag, textContent:'', children:[], dataset:{}, className:'', disabled:false,
      addEventListener(type,fn){handlers.set(`${tag}:${type}`,fn)}, append(...nodes){this.children.push(...nodes)},
      replaceChildren(...nodes){this.children=nodes}, setAttribute(){}}),
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
    resolveSearch: (index, data) => pendingSearches[index](data), searchURLs};
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
