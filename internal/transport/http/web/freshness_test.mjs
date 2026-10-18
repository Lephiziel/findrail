import test from 'node:test';
import assert from 'node:assert/strict';
import { sourceFreshness, sourceHealth } from './freshness.mjs';

test('filesystem distinguishes unscanned, running, failed committed and degraded states', () => {
  const source={id:'fs',kind:'filesystem',documents:2,last_indexed_at:'2026-10-17T12:00:00Z'};
  assert.match(sourceFreshness({...source,last_indexed_at:''}),/No successful update time/);
  assert.match(sourceFreshness(source,{state:'indexing'}),/in progress.*remains searchable/);
  assert.match(sourceFreshness(source,{state:'error',last_success_at:source.last_indexed_at,last_error:'synthetic failure'}),/Last successful update.*failed.*remains searchable/);
  assert.match(sourceFreshness(source,{state:'idle',mode:'polling',watch_warning:'watch unavailable'}),/degraded.*polling/);
  assert.equal(sourceHealth([source],{enabled:true,sources:[]}).state,'warning');
  assert.match(sourceHealth([source],{enabled:true,sources:[{source_id:'fs',state:'idle'}]}).text,/idle does not verify/);
});

test('GitHub is a manual SHA snapshot and archive is frozen without sync-worker errors', () => {
  assert.match(sourceFreshness({kind:'github',documents:1,last_indexed_at:'2026-10-17T12:00:00Z',github:{sha:'abc123'}}),/Manual GitHub snapshot.*abc123/);
  assert.match(sourceFreshness({kind:'archive',documents:1,archive:{imported_at:'2026-10-17T12:00:00Z'}}),/Frozen indexed snapshot.*Imported/);
});

test('API1 missing and unknown optionals produce unknown instead of a green claim', () => {
  assert.match(sourceFreshness({kind:'filesystem',documents:1}),/time available/);
  assert.equal(sourceHealth([{id:'fs',kind:'filesystem'}],{enabled:true,sources:[{source_id:'fs',state:'mystery'}]}).state,'warning');
  assert.equal(sourceHealth(null,null).state,'unknown');
});

test('canceled attempt keeps the prior success timestamp and a committed recovery clears the failure', () => {
  const source={id:'fs',kind:'filesystem',documents:3,last_indexed_at:'2026-10-16T09:00:00Z'};
  const canceled=sourceFreshness(source,{state:'idle',last_success_at:source.last_indexed_at,progress:{attempt_id:'attempt-2',failure_code:'canceled',committed:false}});
  assert.match(canceled,/Last successful update.*Latest attempt did not commit \(canceled\)/);
  const recovered=sourceFreshness({...source,last_indexed_at:'2026-10-18T09:00:00Z'},{state:'idle',last_success_at:'2026-10-18T09:00:00Z',progress:{attempt_id:'attempt-3',committed:true}});
  assert.match(recovered,/Last successful update.*Latest refresh committed/);
  assert.doesNotMatch(recovered,/failed|canceled/i);
});
