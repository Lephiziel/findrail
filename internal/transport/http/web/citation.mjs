const PDF_MEDIA_TYPE = 'application/pdf';

function normalizeLines(value) {
  return value.replace(/\r\n?/g, '\n');
}

function metadata(value, field) {
  if (typeof value !== 'string' || value.trim() === '') {
    throw new TypeError(`evidence.${field} must be a non-empty string`);
  }
  return value.replace(/[\u0000-\u001f\u007f\u2028\u2029]/g, ' ');
}

function escapeMarkdown(value) {
  return value.replace(/[\\`*_{}\[\]<>#]/g, '\\$&');
}

export function sourceURI(value) {
  if (typeof value !== 'string' || value.trim() === '') {
    throw new TypeError('evidence.uri must be a safe absolute source URI');
  }
  if (/[\u0000-\u001f\u007f]/.test(value)) {
    throw new TypeError('evidence.uri contains an unsafe path');
  }
  let parsed;
  try {
    parsed = new URL(value);
  } catch {
    throw new TypeError('evidence.uri must be an absolute source URI');
  }
	if (parsed.protocol === 'https:') {
    // Check the raw authority because URL normalizes an explicit :443 away.
    if (!value.startsWith('https://github.com/') || parsed.username || parsed.password || parsed.port || parsed.hostname !== 'github.com' || parsed.search || parsed.hash) {
      throw new TypeError('evidence.uri must be a commit-pinned GitHub blob permalink');
    }
    const rawPath = value.slice('https://github.com'.length);
    if (rawPath.includes('?') || rawPath.includes('#')) {
      throw new TypeError('evidence.uri must be a commit-pinned GitHub blob permalink');
    }
    const segments = rawPath.split('/').slice(1);
    if (segments.length < 5 || segments[2] !== 'blob' || !/^[0-9a-f]{40}$/i.test(segments[3])) {
      throw new TypeError('evidence.uri must be a commit-pinned GitHub blob permalink');
    }
    if (!/^[A-Za-z0-9](?:[A-Za-z0-9._-]{0,99})$/.test(segments[0]) ||
        !/^[A-Za-z0-9](?:[A-Za-z0-9._-]{0,99})$/.test(segments[1])) {
      throw new TypeError('evidence.uri has invalid repository components');
    }
    for (const [index, segment] of segments.entries()) {
      let decoded;
      try { decoded = decodeURIComponent(segment); } catch { throw new TypeError('evidence.uri has invalid escaping'); }
      if (decoded === '' || decoded === '.' || decoded === '..' || decoded.includes('/') || decoded.includes('\\') || /[\u0000-\u001f\u007f-\u009f\u2028\u2029]/u.test(decoded)) {
        throw new TypeError('evidence.uri contains an unsafe path segment');
      }
      if (index === 2 && decoded !== 'blob') throw new TypeError('evidence.uri has invalid permalink components');
    }
    return parsed.href;
  }
	if (parsed.protocol !== 'file:' || (parsed.hostname !== '' && parsed.hostname !== 'localhost')) {
    throw new TypeError('evidence.uri must use the file: scheme or a commit-pinned GitHub blob permalink');
  }
  if (!/^file:\/\//i.test(value)) {
    throw new TypeError('evidence.uri must be an absolute file URI');
  }
  return parsed.href;
}

function fenceFor(value) {
  let longest = 0;
  let run = 0;
  for (const character of value) {
    if (character === '`') {
      run += 1;
      if (run > longest) longest = run;
    } else {
      run = 0;
    }
  }
  return '`'.repeat(Math.max(3, longest + 1));
}

function selectedText(evidenceText, excerpt) {
  if (excerpt === undefined || excerpt === null) return evidenceText;
  if (typeof excerpt !== 'string') {
    throw new TypeError('options.excerpt must be a string, null, or omitted');
  }
  const normalized = normalizeLines(excerpt);
  if (normalized.trim() === '' || !evidenceText.includes(normalized)) {
    throw new Error('options.excerpt must be a non-empty passage from evidence.text');
  }
  return normalized;
}

export function formatCitation(evidence, options = {}) {
  if (evidence === null || typeof evidence !== 'object') {
    throw new TypeError('evidence must be an object');
  }
  if (options === null || typeof options !== 'object') {
    throw new TypeError('options must be an object');
  }
  const title = metadata(evidence.title, 'title');
  if (typeof evidence.text !== 'string') {
    throw new TypeError('evidence.text must be a string');
  }
  const text = normalizeLines(evidence.text);
  const excerpt = selectedText(text, options.excerpt);
  const uri = sourceURI(evidence.uri);
  const isPDF = evidence.media_type === PDF_MEDIA_TYPE;
  if (isPDF && (!Number.isInteger(evidence.page) || evidence.page < 0)) {
    throw new TypeError('evidence.page must be a non-negative integer for PDF citations');
  }

  const hasPage = isPDF && evidence.page > 0;
  const heading = hasPage ? `${title} — page ${evidence.page}` : title;
  const linkLabel = hasPage ? `${title}, page ${evidence.page}` : title;
  const fence = fenceFor(excerpt);
  const note = evidence.truncated === true
    ? (options.excerpt === undefined || options.excerpt === null
      ? 'Preview truncated; only the visible indexed text is included.'
      : 'Selected passage from a truncated indexed preview.')
    : 'Indexed snapshot.';
  const body = `${fence}text\n${excerpt}${excerpt === '' || excerpt.endsWith('\n') ? '' : '\n'}${fence}`;

  return [
    `## ${escapeMarkdown(metadata(heading, 'title'))}`,
    '',
    body,
    '',
    `Source: [${escapeMarkdown(metadata(linkLabel, 'title'))}](<${uri}>)`,
    '',
    note,
    '',
  ].join('\n');
}
