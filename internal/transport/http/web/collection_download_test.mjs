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
    if (!elements.has(id)) elements.set(id, {value:'',textContent:'',children:[],handlers:{},
      replaceChildren(){this.children=[]},append(...nodes){this.children.push(...nodes)},
      setAttribute(){},addEventListener(type,fn){this.handlers[type]=fn},focus(){},
      querySelector(){const find=node=>typeof node.onclick==='function'?node:node.children?.map(find).find(Boolean);return this.children.map(find).find(Boolean)}});
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
  return {element,window,downloads,notify(type){listeners.get(type)?.()},resolve(data){resolveInventory(data)}};
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

test('temporary edits offer export; writable edits offer Save and survive other status messages', () => {
  const h=harness();
  const title=h.element('collection-title');
  title.oninput({target:title});
  assert.match(h.element('collection-dirty').textContent,/Temporary changes.*Export Markdown/);
  assert.doesNotMatch(h.element('collection-dirty').textContent,/Save/);
  h.window.findrailCollectionPersistence=true;
  h.notify('findrail-collection-persistence');
  assert.match(h.element('collection-dirty').textContent,/Unsaved changes.*Save changes/);
  assert.equal(h.element('collection-save').textContent,'Save changes');
  h.element('collection-status').textContent='Downloaded a copy.';
  assert.match(h.element('collection-dirty').textContent,/Unsaved changes/);
  h.window.findrailSavedCollectionGeneration=h.window.localEvidenceCollection.generation;
  h.notify('persistent-collection-loaded');
  assert.equal(h.element('collection-save').textContent,'Save collection');
  assert.equal(h.element('collection-dirty').textContent,'');
});

test('note and order edits update the separate dirty indicator', () => {
  const h=harness(),c=h.window.localEvidenceCollection;
  h.window.findrailCollectionPersistence=true;
  c.add({...evidence,id:'doc_'+'b'.repeat(64),title:'other.md',text:'Another quote'},'Another quote');
  h.window.findrailSavedCollectionGeneration=c.generation;
  h.notify('persistent-collection-loaded');
  let rows=h.element('collection-list').children;
  const note=rows[0].children.find(node=>'input' in node.handlers);
  note.value='Changed note';note.handlers.input();
  assert.match(h.element('collection-dirty').textContent,/Unsaved changes/);
  h.window.findrailSavedCollectionGeneration=c.generation;
  h.notify('persistent-collection-loaded');
  rows=h.element('collection-list').children;
  const actions=rows[0].children.at(-1);
  actions.children.find(node=>node.textContent==='Move down').onclick();
  assert.equal(c.entries[1].id,evidence.id);
  assert.match(h.element('collection-dirty').textContent,/Unsaved changes/);
});
