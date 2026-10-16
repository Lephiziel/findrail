import assert from 'node:assert/strict';
import test from 'node:test';
import { collectionFilename } from './evidence_collection.mjs';

test('collection filenames preserve meaningful Unicode and normalize separators', () => {
  const cases = [
    ['Retry notes', 'findrail-retry-notes.md'],
    ['PDF / citations: week 1', 'findrail-pdf-citations-week-1.md'],
    ['Заметки', 'findrail-заметки.md'],
    ['  CAFÉ\tNotes  ', 'findrail-café-notes.md'],
    ['Cafe\u0301', 'findrail-café.md'],
    ['日本語 １２３', 'findrail-日本語-１２３.md'],
    ['مرحبا بالعالم', 'findrail-مرحبا-بالعالم.md'],
    ['a<>:"/\\|?*b\u0000\u0085c\u202ed\u2069e', 'findrail-a-b-c-d-e.md'],
    ['CON', 'findrail-con.md'],
    ['Notes.MD.md', 'findrail-notes.md'],
  ];
  for (const [title, expected] of cases) assert.equal(collectionFilename(title), expected);
});

test('unusable collection titles use the evidence fallback', () => {
  for (const title of ['', ' \t ', '...', '../..', '\u202e\u2066', '🦊', '.md', '\u0301']) {
    assert.equal(collectionFilename(title), 'findrail-evidence.md');
  }
});

test('the complete filename is at most 120 UTF-8 bytes without splitting code points', () => {
  for (const point of ['a', 'é', '界', '𐐀']) {
    const name = collectionFilename(point.repeat(200));
    assert.ok(new TextEncoder().encode(name).byteLength <= 120);
    assert.match(name, /^findrail-.+\.md$/u);
    assert.doesNotMatch(name, /\ufffd|--|-\.md/u);
    assert.equal(new TextDecoder('utf-8', {fatal: true}).decode(new TextEncoder().encode(name)), name);
  }
  assert.equal(collectionFilename('a'.repeat(107) + ' 界'), 'findrail-' + 'a'.repeat(107) + '.md');
});
