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
