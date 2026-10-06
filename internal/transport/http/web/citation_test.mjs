import test from 'node:test';
import assert from 'node:assert/strict';
import { formatCitation } from './citation.mjs';

const note = {
  title: 'retry-notes.md',
  uri: 'file:///home/user/notes/retry-notes.md',
  media_type: 'text/markdown',
  text: 'When a webhook arrives twice, use an idempotency key.',
  truncated: false,
};

const pdf = {
  title: 'webhook-runbook.pdf',
  uri: 'file:///home/user/reference/webhook-runbook.pdf#page=2',
  media_type: 'application/pdf',
  page: 2,
  page_count: 2,
  text: 'Idempotency: store the webhook event ID before retrying payment work.',
  truncated: false,
};

test('formats an ordinary note and a PDF citation exactly', () => {
  assert.equal(formatCitation(note), [
    '## retry-notes.md',
    '',
    '```text',
    'When a webhook arrives twice, use an idempotency key.',
    '```',
    '',
    'Source: [retry-notes.md](<file:///home/user/notes/retry-notes.md>)',
    '',
    'Indexed snapshot.',
    '',
  ].join('\n'));
  assert.equal(formatCitation(pdf, { excerpt: 'Idempotency: store the webhook event ID before retrying payment work.' }), [
    '## webhook-runbook.pdf — page 2',
    '',
    '```text',
    'Idempotency: store the webhook event ID before retrying payment work.',
    '```',
    '',
    'Source: [webhook-runbook.pdf, page 2](<file:///home/user/reference/webhook-runbook.pdf#page=2>)',
    '',
    'Indexed snapshot.',
    '',
  ].join('\n'));
});

test('normalizes line endings while preserving whitespace and excerpt boundaries', () => {
  const evidence = { ...note, title: 'Пример.md', text: '  первая\r\n\rвторая\r\n\n  конец  ' };
  assert.match(formatCitation(evidence), /  первая\n\nвторая\n\n  конец  /);
  assert.match(formatCitation(evidence, { excerpt: 'первая\r\n\rвторая' }), /первая\n\nвторая/);
  assert.match(formatCitation({ ...evidence, text: 'Тürkiye · 🦊' }), /Тürkiye · 🦊/);
});

test('keeps markdown and HTML text inside a dynamically sized fence', () => {
  const text = '<script>alert(1)</script>\n[keep](https://example.test)\n```` and ```';
  const result = formatCitation({ ...note, title: 'a*_[b].md', text });
  assert.match(result, /^## a\\\*\\_\\\[b\\\]\.md/m);
  assert.match(result, /`````text\n/);
  assert.match(result, /<script>alert\(1\)<\/script>/);
  assert.match(result, /\[keep\]\(https:\/\/example\.test\)/);
});

test('serializes safe file URIs without double-encoding existing escapes', () => {
  const result = formatCitation({
    ...note,
    uri: 'file:///home/user/a folder/(draft)%20note.md',
  });
  assert.match(result, /file:\/\/\/home\/user\/a%20folder\/\(draft\)%20note\.md/);
  assert.doesNotMatch(result, /%2520/);
});

test('marks full and selected truncated previews differently', () => {
  const evidence = { ...note, truncated: true, text: 'visible text' };
  assert.match(formatCitation(evidence), /Preview truncated; only the visible indexed text is included\./);
  assert.match(formatCitation(evidence, { excerpt: 'visible' }), /Selected passage from a truncated indexed preview\./);
});

test('rejects unsafe metadata, URIs, and excerpts', () => {
  for (const uri of ['javascript:alert(1)', 'data:text/plain,hello', '/tmp/note.md', 'https://example.test/note.md']) {
    assert.throws(() => formatCitation({ ...note, uri }), /file URI|file: scheme/);
  }
  for (const excerpt of ['', '   ', 'not present', 42, {}]) {
    assert.throws(() => formatCitation(note, { excerpt }), /excerpt/);
  }
  assert.throws(() => formatCitation({ ...note, title: ' \n ' }), /title/);
  assert.throws(() => formatCitation({ ...pdf, page: 0 }), /page/);
});

test('uses the whole preview for omitted or null excerpt and preserves empty text', () => {
  assert.equal(formatCitation(note), formatCitation(note, { excerpt: null }));
  const empty = formatCitation({ ...note, text: '' });
  assert.match(empty, /```text\n```/);
  assert.doesNotMatch(empty, /```text\n\n```/);
  assert.equal(empty, formatCitation({ ...note, text: '' }));
});

test('preserves trailing newlines in the fenced body', () => {
  const result = formatCitation({ ...note, text: 'first\n\n' });
  assert.match(result, /```text\nfirst\n\n```/);
});

test('does not mutate inputs and is deterministic', () => {
  const evidence = { ...note, text: 'one\r\ntwo' };
  const options = { excerpt: 'one\rtwo' };
  const beforeEvidence = structuredClone(evidence);
  const beforeOptions = structuredClone(options);
  const first = formatCitation(evidence, options);
  assert.equal(first, formatCitation(evidence, options));
  assert.deepEqual(evidence, beforeEvidence);
  assert.deepEqual(options, beforeOptions);
});
