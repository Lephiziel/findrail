import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import vm from 'node:vm';

const module=readFileSync(new URL('./preview_ui.mjs',import.meta.url),'utf8');
const start=module.indexOf('{\n',module.indexOf('export async function initializePreviewUI('))+2;
const script=module.slice(start,module.lastIndexOf('\n}')).replace(/^if \(!document \|\| initializedDocuments\.has\(document\)\) return;\ninitializedDocuments\.add\(document\);\n/m,'');
const flush=()=>new Promise(resolve=>setImmediate(resolve));

function harness(capability=true,delayedBootstrap=false){
  const listeners=new Map(),elements=new Map();let pendingPost,lastBody;
  const element=id=>{if(!elements.has(id))elements.set(id,{id,hidden:true,textContent:'',children:[],handlers:{},addEventListener(type,fn){this.handlers[type]=fn},append(...xs){this.children.push(...xs)},after(){},setAttribute(){}});return elements.get(id)};
  const window={findrailNativeCapability:{enabled:capability,token:capability?'token':''},addEventListener(t,fn){listeners.set(t,fn)},dispatchEvent(e){listeners.get(e.type)?.(e)}};
  let releaseBootstrap;
  if(delayedBootstrap){delete window.findrailNativeCapability;window.findrailBootstrap=new Promise(resolve=>{releaseBootstrap=()=>{window.findrailNativeCapability={enabled:capability,token:capability?'token':''};resolve()}})}
  class CustomEvent{constructor(type,opts={}){this.type=type;this.detail=opts.detail}}
  const document={querySelector(){return {after(node){elements.set('preview-status',node)}}},querySelectorAll(){return[]},getElementById:element,createElement(tag){return {tag,hidden:false,textContent:'',handlers:{},children:[],addEventListener(type,fn){this.handlers[type]=fn},append(...nodes){this.children.push(...nodes)},setAttribute(){}}}};
  const context=vm.createContext({window,document,CustomEvent,fetch:async(url,opts)=>{
    lastBody=JSON.parse(opts.body);return new Promise(resolve=>{pendingPost=()=>resolve({ok:true,json:async()=>({message:'attempted'})})});
  }});
  vm.runInContext(`(async()=>{${script}})()`,context);
  return{window,element,listeners,releaseBootstrap,get pendingPost(){return pendingPost},get lastBody(){return lastBody}};
}
const identity='a'.repeat(64);

test('native actions wait for asynchronous capability and token bootstrap',async()=>{
  const h=harness(true,true);await flush();
  h.releaseBootstrap();await flush();
  const evidence={id:'doc',source_id:'source',source_kind:'filesystem',original_identity:identity,page:0};
  h.window.dispatchEvent(new CustomEvent('findrail-evidence-preview',{detail:{evidence,generation:1}}));
  assert.equal(h.element('open-original').hidden,false);
  h.element('open-original').onclick();await flush();
  assert.equal(h.lastBody.original_identity,identity);
  h.pendingPost();await flush();
});

test('native original actions are capability-gated and stay bound to preview generation',async()=>{
  const h=harness();await flush();
  const evidence={id:'doc-a',source_id:'src',source_kind:'filesystem',original_identity:identity,page:2};
  h.window.dispatchEvent(new CustomEvent('findrail-evidence-preview',{detail:{evidence,generation:1}}));
  const open=h.element('open-original');assert.equal(open.hidden,false);open.onclick();await flush();
  h.window.dispatchEvent(new CustomEvent('findrail-evidence-preview',{detail:{evidence:{...evidence,id:'doc-b'},generation:2}}));
  h.pendingPost();await flush();
  assert.equal(h.element('open-original').hidden,false);
  assert.equal(h.element('preview-status').textContent, '');
});

test('read-only capability keeps preview opener actions hidden',async()=>{
  const h=harness(false);await flush();
  const evidence={id:'doc',source_id:'src',source_kind:'filesystem',original_identity:identity,page:0};
  h.window.dispatchEvent(new CustomEvent('findrail-evidence-preview',{detail:{evidence,generation:1}}));
  assert.equal(h.element('open-original').hidden,true);
  assert.equal(h.element('open-folder').hidden,true);
});

test('result action sends only document identity, action, and indexed page',async()=>{
  const h=harness();await flush();const card={children:[],append(...nodes){this.children.push(...nodes)}};
  const item={id:'doc-result',source_id:'source',source_kind:'filesystem',original_identity:identity,page:4};
  h.window.dispatchEvent(new CustomEvent('findrail-result-created',{detail:{item,card}}));
  const actionBox=card.children[0];assert.deepEqual(actionBox.children.map(x=>x.textContent),['Open original','Open containing folder']);
  actionBox.children[0].handlers.click();await flush();
  assert.deepEqual(h.lastBody,{document_id:'doc-result',original_identity:identity,action:'file',page:4});
  assert.equal(Object.hasOwn(h.lastBody,'path'),false);
});

function CustomEvent(type,opts={}){this.type=type;this.detail=opts.detail}
