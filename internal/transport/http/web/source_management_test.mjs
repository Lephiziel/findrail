import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import vm from 'node:vm';

const html = readFileSync(new URL('./index.html', import.meta.url), 'utf8');
const script = [...html.matchAll(/<script type="module">([\s\S]*?)<\/script>/g)][0][1]
  .replace(/^import .*;$/m, '');
const flush = () => new Promise(resolve => setImmediate(resolve));

function harness() {
  const elements = new Map(), handlers = new Map(), timers = new Map();
  let nextTimer = 0, requests = 0, pendingEvidence;
  function element(id) {
    if (!elements.has(id)) elements.set(id, {
      value: '', textContent: '', hidden: false, disabled: false, open: false,
      addEventListener(type, fn) { handlers.set(`${id}:${type}`, fn); },
      replaceChildren() { this.value = ''; }, append() {}, contains() { return false; },
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
    createElement: element, createTextNode: text => ({textContent: text}),
  };
  const context = vm.createContext({window, document, AbortController, URLSearchParams,
    Event,
    setTimeout: schedule, clearTimeout: id => timers.delete(id),
    Option: function(text, value) { this.text = text; this.value = value; },
    async fetch(url) {
      requests++;
      if (url.startsWith('/api/v1/documents/')) return new Promise(resolve => {
        pendingEvidence = data => resolve({ok: true, json: async () => data});
      });
      return {ok: true, json: async () => url.includes('/sync')
        ? {enabled: true, sources: []} : {sources: []}};
    },
  });
  vm.runInContext(script, context);
  return {context, timers, element, document, handlers,
    notify: (type, detail) => handlers.get(`window:${type}`)?.({detail}),
    get requests() { return requests; },
    resolveEvidence: data => pendingEvidence(data)};
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
