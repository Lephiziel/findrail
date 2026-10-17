import test from 'node:test';
import assert from 'node:assert/strict';
import { decodeSearchPreset, encodeSearchPreset } from './search_ui.mjs';

const sample={format:'findrail-search-preset',version:1,title:'Emoji 🔎',query:'"café 🧭"',mode:'advanced',filters:{format:'pdf',path_prefix:'notes/📁',title_contains:'Résumé'},source:{scope:'source',id_hint:'source_123',display_name:'Synthetic source'}};
test('preset codec round-trips exact Unicode strings and emits deterministic JSON',()=>{
 const encoded=encodeSearchPreset(sample);assert.equal(encoded,encodeSearchPreset(sample));assert.deepEqual(decodeSearchPreset(encoded),sample);assert.deepEqual(decodeSearchPreset(new TextEncoder().encode(encoded)),sample);
});
test('preset codec accepts explicit all scope and empty filters',()=>{
 const p={...sample,mode:'literal',filters:{format:'all',path_prefix:'',title_contains:''},source:{scope:'all',id_hint:'',display_name:''}};assert.deepEqual(decodeSearchPreset(encodeSearchPreset(p)),p);
});
test('preset codec rejects duplicate, unknown, hostile, and missing nested fields',()=>{
 const valid=JSON.stringify(sample);for(const raw of [valid.replace('"version":1','"version":1,"version":1'),valid.replace('"format":"pdf"','"__proto__":"x","format":"pdf"'),valid.replace('"title_contains":"Résumé"','"extra":0,"title_contains":"Résumé"'),valid.replace('"display_name":"Synthetic source"','"constructor":"x","display_name":"Synthetic source"'),valid.replace(',"display_name":"Synthetic source"','')])assert.throws(()=>decodeSearchPreset(raw));
});
test('preset codec bounds raw bytes and nesting and validates values',()=>{
 for(const raw of [' '.repeat(16385),'{"x":'.repeat(8)+'0'+']'.repeat(0),'{"format":"findrail-search-preset","version":1.0}',JSON.stringify({...sample,version:2}),JSON.stringify({...sample,mode:'wat'}),JSON.stringify({...sample,filters:{...sample.filters,format:'wat'}}),JSON.stringify({...sample,title:'x'.repeat(257)}),JSON.stringify({...sample,query:'😀'.repeat(257)}),JSON.stringify({...sample,filters:{...sample.filters,path_prefix:'x'.repeat(513)}}),JSON.stringify({...sample,filters:{...sample.filters,title_contains:'x'.repeat(129)}})])assert.throws(()=>decodeSearchPreset(raw));
 assert.throws(()=>decodeSearchPreset(Uint8Array.of(0xff)),/UTF-8/);assert.throws(()=>decodeSearchPreset(encodeSearchPreset(sample)+' ' .repeat(16384)),/16 KiB/);
});


test('encoding is canonical regardless of input object key order',()=>{
 const reordered={source:{display_name:sample.source.display_name,id_hint:sample.source.id_hint,scope:sample.source.scope},filters:{title_contains:sample.filters.title_contains,path_prefix:sample.filters.path_prefix,format:sample.filters.format},mode:sample.mode,query:sample.query,title:sample.title,version:sample.version,format:sample.format};
 assert.equal(encodeSearchPreset(reordered),encodeSearchPreset(sample));
});

test('raw JavaScript strings cannot silently replace invalid Unicode scalars',()=>{
 const raw=JSON.stringify(sample).replace(sample.title,'invalid '+String.fromCharCode(0xd800));
 assert.throws(()=>decodeSearchPreset(raw),/Unicode|UTF/);
});

test('strict tokens reject escaped duplicate keys, non-integer versions, wrong types and excessive valid nesting',()=>{
 const raw=JSON.stringify(sample);
 for(const value of [raw.replace('"version":1','"version":1.0'),raw.replace('"version":1','"version":1e0'),raw.replace('"format":"pdf"','"format":"pdf","for\\u006dat":"pdf"'),JSON.stringify({...sample,filters:null}),JSON.stringify({...sample,query:3}),JSON.stringify({...sample,source:{...sample.source,id_hint:'a\0b'}}),JSON.stringify({...sample,query:'\ud800'})])assert.throws(()=>decodeSearchPreset(value));
 assert.throws(()=>decodeSearchPreset('['.repeat(7)+'0'+']'.repeat(7)),/deep/);
});

test('UTF-8 and code-point bounds retain exact valid boundary strings',()=>{
 const boundary={...sample,title:'я'.repeat(128),query:'😀'.repeat(256),filters:{...sample.filters,path_prefix:'📁'.repeat(512),title_contains:'🧭'.repeat(128)},source:{...sample.source,display_name:'я'.repeat(256)}};
 assert.deepEqual(decodeSearchPreset(encodeSearchPreset(boundary)),boundary);
 for(const change of [{title:boundary.title+'x'},{query:boundary.query+'x'},{source:{...boundary.source,display_name:boundary.source.display_name+'x'}}])assert.throws(()=>encodeSearchPreset({...boundary,...change}));
});
