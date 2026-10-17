import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';

const html = readFileSync(new URL('./index.html', import.meta.url), 'utf8');
const docs = readFileSync(new URL('../../../../docs/portable-snapshots.md', import.meta.url), 'utf8');

test('portable source UI uses native host paths and gated production endpoints', () => {
  assert.match(html, /id="snapshot-inspect-open"[^>]*hidden/);
  assert.match(html, /Absolute archive\/output path on the computer where Findrail is running/);
  assert.doesNotMatch(html, /id="snapshot-path"[^>]*type="file"/);
  assert.match(html, /\/api\/v1\/snapshots\/inspect/);
  assert.match(html, /\/api\/v1\/snapshots\/inspections\//);
  assert.match(html, /\/api\/v1\/snapshots\/import/);
  assert.match(html, /\/snapshot-export/);
  assert.match(html, /flow\.exportSummary=true/);
  assert.match(html, /Snapshot exported and verified/);
  assert.match(html, /capabilities\.portable_snapshots===true/);
  assert.match(html, /new TextEncoder\(\)\.encode\(value\)\.length<=4096/);
  assert.match(html, /new TextEncoder\(\)\.encode\(name\)\.length>512/);
});

test('portable UI protects claims and late results with a generation and one active flow', () => {
  assert.match(html, /portableGeneration/);
  assert.match(html, /generation!==portableGeneration\|\|!snapshotDialog\.open/);
  assert.match(html, /portableFlow\?\.jobId\|\|portableFlow\?\.busy/);
  assert.match(html, /snapshotDialog\.addEventListener\('close',\(\)=>\{invalidatePortable\(\)/);
  assert.match(html, /Path changed\. Inspect this archive again before importing\./);
  assert.match(html, /already_imported:/);
});

test('user docs distinguish source archives from evidence bundles and state bounds', () => {
  assert.match(docs, /not a browser file picker or upload/);
  assert.match(docs, /evidence-bundle workflow/);
  assert.match(docs, /32 MiB compressed, 64 MiB expanded, 2000 documents and 20000 pages/);
  assert.match(docs, /alpha\.4 feature/);
});
