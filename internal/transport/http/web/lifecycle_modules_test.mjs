import assert from 'node:assert/strict';
import test from 'node:test';

test('lifecycle modules export explicit initializers and are inert when imported in Node', async () => {
  const requests = [];
  const originalFetch = globalThis.fetch;
  globalThis.fetch = (...args) => { requests.push(args); throw new Error('unexpected import-time request'); };
  try {
    const [search, preview, collections] = await Promise.all([
      import('./search_ui.mjs'),
      import('./preview_ui.mjs'),
      import('./collections_ui.mjs'),
    ]);
    assert.equal(typeof search.initializeSearchUI, 'function');
    assert.equal(typeof preview.initializePreviewUI, 'function');
    assert.equal(typeof collections.initializeCollectionsUI, 'function');
    assert.equal(typeof collections.initializeSavedCollectionsUI, 'function');
    assert.deepEqual(requests, []);
  } finally {
    globalThis.fetch = originalFetch;
  }
});
