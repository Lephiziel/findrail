import assert from 'node:assert/strict';
import test from 'node:test';
import { BundleImportState } from './collections_ui.mjs';

test('file, mappings, inventory, and dialog transitions invalidate preview claims and late responses', () => {
  const state = new BundleImportState();
  const fileGeneration = state.advance();
  assert.equal(state.acceptClaim(fileGeneration, 'claim-file'), true);
  const mappingGeneration = state.advance();
  assert.equal(state.claim, '');
  assert.equal(state.isCurrent(fileGeneration), false);
  assert.equal(state.acceptClaim(fileGeneration, 'late-claim'), false);
  assert.equal(state.acceptClaim(mappingGeneration, 'claim-mapped'), true);
  const inventoryGeneration = state.advance();
  assert.equal(state.claim, '');
  assert.equal(state.acceptClaim(mappingGeneration, 'late-mapping-claim'), false);
  const nextDialog = state.advance();
  assert.equal(state.isCurrent(inventoryGeneration), false);
  assert.equal(state.acceptClaim(nextDialog, ''), false);
  assert.equal(state.claim, '');
});

test('a refreshed preview claim is scoped to the newest request only', () => {
  const state = new BundleImportState();
  const first = state.advance(), second = state.advance();
  assert.equal(state.acceptClaim(first, 'stale'), false);
  assert.equal(state.acceptClaim(second, 'fresh'), true);
  assert.equal(state.claim, 'fresh');
});
