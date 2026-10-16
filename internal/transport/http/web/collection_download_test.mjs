import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import vm from 'node:vm';
import { EvidenceCollection, LIMITS, validDocumentID, compareCurrentSnapshot, collectionFilename } from './evidence_collection.mjs';

const source = readFileSync(new URL('./collections_ui.mjs', import.meta.url), 'utf8')
  .replace(/^import .*;$/m, '').replace(/^export /gm, '');
const evidence = {id:'doc_'+'a'.repeat(64),source_id:'fictional-source',source_name:'Demo',source_kind:'filesystem',
  title:'note.md',path:'note.md',uri:'file:///fictional/note.md',media_type:'text/markdown',
  content_hash:'synthetic',modified_at:'2026-10-01T00:00:00Z',text:'Fictional passage',truncated:false};

function harness() {
  const elements = new Map(), listeners = new Map(), downloads = [];
  let resolveInventory, blob;
  const element = id => {
    if (!elements.has(id)) elements.set(id, {value:'',textContent:'',children:[],
      replaceChildren(){this.children=[]},append(...nodes){this.children.push(...nodes)},
      setAttribute(){},addEventListener(){},focus(){}});
    return elements.get(id);
  };
  const document = {getElementById:element,createElement:tag=>tag==='a'
    ? {click(){downloads.push({name:this.download,blob})}} : element(Symbol(tag))};
  const window = {addEventListener:(type,fn)=>listeners.set(type,fn)};
  const context = vm.createContext({document,window,EvidenceCollection,LIMITS,validDocumentID,
    compareCurrentSnapshot,collectionFilename,AbortController,TextEncoder,TextDecoder,Blob,
    URL:{createObjectURL(value){blob=value;return 'blob:fictional'},revokeObjectURL(){}},
    setTimeout(){return 1},clearTimeout(){},
    fetch:()=>new Promise(resolve=>{resolveInventory=data=>resolve(new Response(JSON.stringify(data)))})});
  vm.runInContext(source+'\ninitializeCollectionsUI({document,window,fetch});',context);
  window.localEvidenceCollection.add(evidence,evidence.text);
  element('collection-title').value='Retry notes';
  return {element,window,downloads,resolve(data){resolveInventory(data)}};
}
const inventory = {sources:[{id:'fictional-source',kind:'filesystem',name:'Demo',root:'/fictional',documents:1}]};

test('download uses one collection title for the filename, Markdown and success message', async () => {
  const h=harness(), pending=h.element('collection-download').onclick();
  h.resolve(inventory); await pending;
  assert.equal(h.downloads.length,1);
  assert.equal(h.downloads[0].name,'findrail-retry-notes.md');
  assert.match(await h.downloads[0].blob.text(),/^# Retry notes\n/);
  assert.equal(h.element('collection-status').textContent,'Downloaded findrail-retry-notes.md.');
});

test('a title change during inventory preparation stops the delayed download', async () => {
  const h=harness(), pending=h.element('collection-download').onclick();
  h.element('collection-title').value='New title';
  h.element('collection-title').oninput({target:h.element('collection-title')});
  h.resolve(inventory); await pending;
  assert.equal(h.downloads.length,0);
  assert.match(h.element('collection-status').textContent,/Collection changed/);
});

test('a disappeared source is purged and cannot be downloaded', async () => {
  const h=harness(), pending=h.element('collection-download').onclick();
  h.resolve({sources:[]}); await pending;
  assert.equal(h.downloads.length,0);
  assert.equal(h.window.localEvidenceCollection.entries.length,0);
  assert.match(h.element('collection-status').textContent,/source disappeared/);
});

test('failed saved-citation validation still stops the download', async () => {
  const h=harness();
  h.window.findrailValidateSavedCollection=async()=>false;
  await h.element('collection-download').onclick();
  assert.equal(h.downloads.length,0);
});
