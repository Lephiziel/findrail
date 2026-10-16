import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import vm from 'node:vm';
import test from 'node:test';
import {EvidenceCollection, LIMITS, validDocumentID, compareCurrentSnapshot} from './evidence_collection.mjs';

const source=readFileSync(new URL('./collections_ui.mjs',import.meta.url),'utf8').replace(/^import .*;$/m,'').replace(/^export /gm,'');
const flush=()=>new Promise(resolve=>setImmediate(resolve));
const bundle={format:'findrail-evidence-bundle',version:1,title:'Synthetic import',sources:[{alias:'s1',kind:'filesystem',display_name:'Notes'}],entries:[{source_alias:'s1',relative_path:'note.md',media_type:'text/plain',content_hash:'a'.repeat(64),page:0,start_char:0,text:'synthetic quote',note:'synthetic note'}]};

async function harness(){
 const elements=new Map(), windowHandlers=new Map(), documentHandlers=new Map(), requests=[], pendingImports=[], pendingPreviews=[];
 let collections=[], delayList=null;
 const descendants=node=>node.children.flatMap(child=>[child,...descendants(child)]);
 function node(tag='div'){
  return {tagName:tag.toUpperCase(),value:'',textContent:'',hidden:false,disabled:false,checked:false,open:false,dataset:{},children:[],handlers:new Map(),
   append(...children){this.children.push(...children)},add(child){this.children.push(child)},replaceChildren(...children){this.children=children;this.value=''},
   addEventListener(type,fn){this.handlers.set(type,fn)},setAttribute(){},querySelectorAll(selector){return descendants(this).filter(child=>child.tagName===selector.toUpperCase())},
   focus(){document.activeElement=this},showModal(){this.open=true},close(){this.open=false;this.handlers.get('close')?.()},
  };
 }
 const element=id=>{if(!elements.has(id))elements.set(id,node());return elements.get(id)};
 const document={getElementById:element,createElement:node,querySelectorAll:()=>[],addEventListener(type,fn){documentHandlers.set(type,fn)}};
 const window={localEvidenceCollection:new EvidenceCollection(),addEventListener(type,fn){windowHandlers.set(type,fn)},dispatchEvent(event){windowHandlers.get(event.type)?.(event)}};
 const response=data=>new Response(JSON.stringify(data),{status:200});
 async function fetch(url,options){
  requests.push({url,options});
  if(url.endsWith('/capabilities'))return response({collection_persistence:true,evidence_bundle:true});
  if(url.endsWith('/session'))return response({token:'synthetic'});
  if(url.endsWith('/sources'))return response({sources:[{id:'destination',name:'Notes',kind:'filesystem'}]});
  if(url.endsWith('/import/preview'))return new Promise(resolve=>pendingPreviews.push(data=>resolve(response(data))));
  if(url.endsWith('/import'))return new Promise((resolve,reject)=>pendingImports.push({resolve:data=>resolve(response(data)),reject}));
  if(url==='/api/v1/collections'){
   if(delayList)return new Promise(resolve=>{delayList=data=>resolve(response(data))});
   return response({collections});
  }
  if(url.startsWith('/api/v1/collections/'))return response({id:url.split('/').at(-1),revision:1,title:'Imported result',entries:[]});
  throw Error('Unexpected request '+url);
 }
 const context=vm.createContext({document,window,fetch,EvidenceCollection,LIMITS,validDocumentID,compareCurrentSnapshot,AbortController,TextEncoder,TextDecoder,Event,CustomEvent:class extends Event{constructor(type,{detail}){super(type);this.detail=detail}},Option:function(text,value){return Object.assign(node('option'),{textContent:text,value})},setTimeout,clearTimeout});
 vm.runInContext(source+'\ninitializeSavedCollectionsUI({document,window,fetch});',context);
 const fire=(id,type)=>element(id).handlers.get(type)?.({target:element(id)});
 await documentHandlers.get('DOMContentLoaded')();
 async function open(){await element('collection-import-bundle').onclick();element('bundle-file').files=[{size:1000,name:'synthetic.json',type:'application/json',arrayBuffer:async()=>new TextEncoder().encode(JSON.stringify(bundle)).buffer}];await fire('bundle-file','change');const select=element('bundle-mappings').querySelectorAll('select')[0];select.value='destination';select.handlers.get('change')();}
 async function preview(){const pending=fire('bundle-preview','click');pendingPreviews.shift()({entries:[{index:0,status:'match'}],claim:'synthetic-claim'});await pending;}
 function confirm(){element('bundle-confirm').checked=true;fire('bundle-confirm','change');}
 await open();await preview();confirm();
 return {element,fire,window,document,requests,pendingImports,pendingPreviews,preview,confirm,setCollections(value){collections=value},holdList(){delayList=true},resolveList(data){delayList(data);delayList=null}};
}

test('pending import cannot be submitted again by editing title or confirmation',async()=>{
 const h=await harness(),pending=h.fire('bundle-commit','click');
 h.element('bundle-title').value='Another title';h.fire('bundle-title','input');h.confirm();
 assert.equal(h.element('bundle-commit').disabled,true);
 h.fire('bundle-commit','click');
 assert.equal(h.requests.filter(r=>r.url==='/api/v1/collections/import').length,1);
 h.setCollections([{id:'new',title:'Imported result',entry_count:0}]);h.pendingImports[0].resolve({id:'new'});await pending;
 assert.equal(h.document.activeElement,h.element('collection-title'));
});

test('every new preview clears the prior confirmation before its response arrives',async()=>{
 const h=await harness(),pending=h.fire('bundle-preview','click');
 assert.equal(h.element('bundle-confirm').checked,false);
 assert.equal(h.element('bundle-commit').disabled,true);
 h.pendingPreviews.shift()({entries:[{index:0,status:'match'}],claim:'fresh'});await pending;
 assert.equal(h.element('bundle-commit').disabled,true);
});

test('network loss during import refreshes collections and explains an uncertain commit',async()=>{
 const h=await harness(),before=h.requests.filter(r=>r.url==='/api/v1/collections').length,pending=h.fire('bundle-commit','click');
 h.pendingImports[0].reject(new TypeError('Network unavailable'));await pending;
 assert.ok(h.requests.filter(r=>r.url==='/api/v1/collections').length>before);
 assert.match(h.element('bundle-status').textContent,/outcome.*uncertain|may have.*completed/i);
 assert.equal(h.element('bundle-commit').disabled,true);
});

test('edits made while the post-import collection refresh is delayed are retained',async()=>{
 const h=await harness(),pending=h.fire('bundle-commit','click');h.holdList();h.pendingImports[0].resolve({id:'new'});await flush();
 h.window.localEvidenceCollection.setTitle('Newer local edits');
 h.resolveList({collections:[{id:'new',title:'Imported result',entry_count:0}]});await pending;
 assert.equal(h.window.localEvidenceCollection.title,'Newer local edits');
 assert.match(h.element('collection-status').textContent,/kept|not replaced/i);
});


test('cancel followed by a late successful commit retains the current local collection',async()=>{
 const h=await harness(),pending=h.fire('bundle-commit','click');
 h.element('bundle-import').close();
 h.window.localEvidenceCollection.setTitle('Continued local work');
 h.setCollections([{id:'late',title:'Imported result',entry_count:0}]);
 h.pendingImports[0].resolve({id:'late'});await pending;
 assert.equal(h.window.localEvidenceCollection.title,'Continued local work');
 assert.equal(h.element('bundle-file').value,'');
 assert.equal(h.element('bundle-commit').disabled,true);
 assert.match(h.element('collection-status').textContent,/Import completed.*not replaced/);
});
