import { formatCitation } from './citation.mjs';

export const LIMITS = Object.freeze({ entries: 40, preview: 256 * 1024, text: 2 * 1024 * 1024, metadata: 16 * 1024, note: 2048, title: 256, markdown: 4 * 1024 * 1024 });
const STATUSES = new Set(['not checked','unchanged at last check','changed/current version differs','unavailable','source removed']);
const encoder = new TextEncoder();
const bytes = value => encoder.encode(value).byteLength;
const cleanLines = value => value.replace(/\r\n?/g, '\n');
const jsonCopy = value => JSON.parse(JSON.stringify(value));
function literalFence(value) { let longest=0,run=0;for(const ch of value){if(ch==='~'){run++;longest=Math.max(longest,run)}else run=0}return '~'.repeat(Math.max(3,longest+1)); }

export function collectionFilename(title) {
  const prefix = 'findrail-', suffix = '.md';
  const normalized = String(title).normalize('NFC').toLowerCase().trim()
    .replace(/(?:\.md)+$/u, '')
    .replace(/[^\p{L}\p{N}\p{M}]+/gu, '-').replace(/^-+|-+$/g, '');
  let stem = '', size = 0;
  for (const point of normalized) {
    const length = bytes(point);
    if (size + length > 120 - bytes(prefix + suffix)) break;
    stem += point; size += length;
  }
  stem = stem.replace(/-+$/g, '');
  if (!/[\p{L}\p{N}]/u.test(stem)) stem = 'evidence';
  return prefix + stem + suffix;
}

export function safeDecoration(value) {
  return String(value).replace(/[\u0000-\u001f\u007f\u2028\u2029\u202a-\u202e\u2066-\u2069]/g, ' ').replace(/([\\`*_{}\[\]<>#])/g, '\\$1');
}

export class EvidenceCollection {
  #entries = [];
  #title = 'Findrail evidence collection';
  #generation = 0;
  get generation() { return this.#generation; }
  get title() { return this.#title; }
  get entries() { return jsonCopy(this.#entries); }
  restore(title, entries) {
    if (!Array.isArray(entries) || entries.length > LIMITS.entries || typeof title !== 'string' || !title.trim() || bytes(title) > LIMITS.title) throw new TypeError('Invalid saved collection.');
    let text=0, metadata=0;
    for (const x of entries) {
      if (!x || typeof x.key !== 'string' || typeof x.text !== 'string' || typeof x.excerpt !== 'string' || typeof x.note !== 'string' || bytes(x.text)>LIMITS.preview || bytes(x.note)>LIMITS.note || !x.text.includes(x.excerpt)) throw new TypeError('Invalid saved collection entry.');
      text+=bytes(x.text)+bytes(x.excerpt);const meta=bytes(JSON.stringify({...x,text:'',excerpt:'',note:''}));if(meta>LIMITS.metadata)throw new RangeError('Saved entry metadata exceeds 16 KiB.');metadata+=meta;
    }
    if(text>LIMITS.text||metadata>LIMITS.entries*LIMITS.metadata)throw new RangeError('Saved collection exceeds limits.');
    this.#title=title;this.#entries=jsonCopy(entries);this.#generation++;
  }
  setTitle(title) {
    if (typeof title !== 'string' || !title.trim() || bytes(title) > LIMITS.title) throw new RangeError('Collection title must be non-empty and at most 256 UTF-8 bytes.');
    this.#title = title; this.#generation++;
  }
  add(evidence, excerpt, options = {}) {
    if (!evidence || typeof evidence !== 'object' || typeof excerpt !== 'string') throw new TypeError('A visible evidence snapshot and passage are required.');
    for (const field of ['id','source_id','source_name','source_kind','title','path','uri','media_type','content_hash','modified_at']) {
      if (typeof evidence[field] !== 'string') throw new TypeError(`Evidence ${field} must be text.`);
    }
    if (!validDocumentID(evidence.id) || evidence.source_id.length===0 || bytes(evidence.source_id)>512 || /[\u0000-\u001f\u007f]/.test(evidence.source_id)) throw new TypeError('Evidence document/source IDs are invalid.');
    if (!Number.isInteger(evidence.page ?? 0) || (evidence.page ?? 0)<0 || !Number.isInteger(evidence.page_count ?? 0) || (evidence.page_count ?? 0)<0) throw new TypeError('Evidence page metadata is invalid.');
    if (evidence.media_type==='application/pdf' && (evidence.page_count<1 || evidence.page>evidence.page_count)) throw new TypeError('PDF evidence page binding is invalid.');
    if (evidence.media_type!=='application/pdf' && ((evidence.page??0)!==0 || (evidence.page_count??0)!==0)) throw new TypeError('Non-PDF evidence cannot claim a page.');
    const text = cleanLines(evidence.text);
    const passage = cleanLines(excerpt);
    if (!passage.trim() || !text.includes(passage)) throw new TypeError('Choose a non-empty exact passage from the visible preview.');
    if (bytes(text) > LIMITS.preview) throw new RangeError('Preview exceeds the 256 KiB per-entry limit.');
    formatCitation({...evidence,text,truncated:evidence.truncated===true,page:evidence.page??0},{excerpt:passage});
    const snapshot = { id:evidence.id, source_id:evidence.source_id, source_name:evidence.source_name, source_kind:evidence.source_kind, title:evidence.title, path:evidence.path, uri:evidence.uri, media_type:evidence.media_type, content_hash:evidence.content_hash, modified_at:evidence.modified_at, page:evidence.page ?? 0, page_count:evidence.page_count ?? 0, text_start_char:evidence.text_start_char ?? 0, text_total_chars:evidence.text_total_chars ?? 0, text, truncated:evidence.truncated === true, excerpt:passage, whole_preview:options.wholePreview===true, note:'', collected_at:new Date().toISOString(), status:'not checked' };
    snapshot.original_identity=evidence.original_identity || "";
    snapshot.key=crypto.randomUUID();
    const metadata={...snapshot,text:'',excerpt:'',note:''};
    if (bytes(JSON.stringify(metadata)) > LIMITS.metadata) throw new RangeError('Evidence metadata exceeds 16 KiB.');
    const duplicate = this.#entries.find(item => item.source_id===snapshot.source_id && item.id===snapshot.id && item.content_hash===snapshot.content_hash && item.page===snapshot.page && item.excerpt===snapshot.excerpt);
    if (duplicate) return { duplicate:true, key:duplicate.key };
    if (this.#entries.length >= LIMITS.entries) throw new RangeError('Collection is full (40 entries); remove an entry before adding evidence.');
    const retained = this.#entries.reduce((sum,item)=>sum+bytes(item.text)+bytes(item.excerpt),0)+bytes(text)+bytes(passage);
    if (retained > LIMITS.text) throw new RangeError('Collection text exceeds 2 MiB; remove entries before adding this preview.');
    this.#entries.push(snapshot); this.#generation++;
    return { duplicate:false, key:snapshot.key };
  }
  note(key, note) {
    if (typeof note !== 'string' || bytes(note)>LIMITS.note) throw new RangeError('Note must be at most 2,048 UTF-8 bytes.');
    const item=this.#entries.find(x=>x.key===key); if(!item) return false;
    item.note=note; this.#generation++; return true;
  }
  move(key, delta) { const i=this.#entries.findIndex(x=>x.key===key), j=i+delta; if(i<0||j<0||j>=this.#entries.length)return false; [this.#entries[i],this.#entries[j]]=[this.#entries[j],this.#entries[i]];this.#generation++;return true; }
  remove(key) { const n=this.#entries.length;this.#entries=this.#entries.filter(x=>x.key!==key);if(n!==this.#entries.length)this.#generation++;return n!==this.#entries.length; }
  purgeSource(id) { const n=this.#entries.length;this.#entries=this.#entries.filter(x=>x.source_id!==id);if(n!==this.#entries.length)this.#generation++;return n-this.#entries.length; }
  clear() { if(this.#entries.length){this.#entries=[];this.#generation++;} }
  mark(key,status) { if(!STATUSES.has(status))throw new TypeError('Unknown snapshot verification status.');const item=this.#entries.find(x=>x.key===key);if(item){item.status=status;this.#generation++;} }
  export(title=this.#title, at=new Date().toISOString()) {
    if(!this.#entries.length)return '';
    if(typeof title!=='string'||!title.trim()||bytes(title)>LIMITS.title)throw new RangeError('Collection title must be non-empty and at most 256 UTF-8 bytes.');
    const parts=[`# ${safeDecoration(title)}`,``, `Exported ${safeDecoration(at)} as a session snapshot. Freshness is only the index state at the last check.`,``,...this.#entries.flatMap(item=>{
      const citation=item.whole_preview?formatCitation(item):formatCitation(item,{excerpt:item.excerpt});
      const fence=literalFence(item.note);
      const note=item.note?`User note:\n\n${fence}text\n${item.note.replace(/\r\n?/g,'\n')}\n${fence}\n\n`:'';
      const uriFence=literalFence(item.uri);
      const metadata=`Source: ${item.source_name}\nKind: ${item.source_kind}\nHash: ${item.content_hash}\nCollected: ${item.collected_at}\nVerification: ${item.status}`;
      const metadataFence=literalFence(metadata);
      return [citation,`Snapshot metadata (literal text):\n\n${metadataFence}text\n${metadata}\n${metadataFence}`, `Historical URI (plain provenance, not a public download link):\n\n${uriFence}text\n${item.uri}\n${uriFence}${item.truncated?'\n\nVisible preview was truncated; this is not the full document.':''}`,note];
    })];
    const output=parts.join('\n');if(bytes(output)>LIMITS.markdown)throw new RangeError('Markdown export exceeds 4 MiB; remove entries or shorten notes.');return output;
  }
}

// Filesystem/GitHub connector IDs use doc_<sha256>; imported archive IDs are a
// SHA-256 without the prefix because the importing source is part of the hash.
export function validDocumentID(id) { return typeof id==='string' && /^(?:doc_)?[0-9a-f]{64}$/.test(id); }

export function compareCurrentSnapshot(saved, current) {
  if (!current || typeof current!=='object' ||
      !['id','source_id','title','uri','path','source_name','source_kind','media_type','content_hash','modified_at','text'].every(key=>typeof current[key]==='string') ||
      typeof current.truncated!=='boolean' || !Number.isInteger(current.page??0) || !Number.isInteger(current.page_count??0) ||
      (current.page??0)<0 || (current.page_count??0)<0) return 'unavailable';
  if(current.id!==saved.id||current.source_id!==saved.source_id||(current.page??0)!==(saved.page??0))return 'unavailable';
  const same=['content_hash','title','uri','path','source_name','source_kind','media_type','modified_at'].every(key=>current[key]===saved[key]) && current.truncated===saved.truncated && (current.page_count??0)===(saved.page_count??0);
  if((current.text_start_char??0)!==(saved.text_start_char??0))return 'changed/current version differs';
  return same?'unchanged at last check':'changed/current version differs';
}
