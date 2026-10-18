const validTime = value => typeof value === 'string' && value !== '' && Number.isFinite(Date.parse(value));
const time = value => validTime(value) ? new Date(value).toLocaleString() : '';

// Pure presentation helpers: absent optional API fields stay unknown, never green.
export function sourceFreshness(source, watcher) {
  if (!source || typeof source !== 'object') return 'Index status unavailable. Retry to check.';
  const docs = Number.isSafeInteger(source.documents) ? `${source.documents} documents` : 'Document count unavailable';
  if (source.kind === 'archive') {
    const imported = time(source.archive?.imported_at);
    const original = time(source.archive?.original_indexed_at);
    const origin = source.archive?.origin?.kind || 'unknown';
    const fingerprint = typeof source.archive?.fingerprint === 'string' ? source.archive.fingerprint : '';
    return `Frozen indexed snapshot · ${docs} · Origin ${origin}${fingerprint ? ` · Fingerprint ${fingerprint}` : ''}${original ? ` · Original indexed ${original}` : ''}${imported ? ` · Imported ${imported}` : ' · Import time unavailable'} · No refresh or configure`;
  }
  if (source.kind === 'github') {
    const sha = typeof source.github?.sha === 'string' ? source.github.sha : '';
    const indexed = time(source.last_indexed_at);
    return `Manual GitHub snapshot · ${docs}${sha ? ` · Snapshot ${sha}` : ' · Snapshot SHA unavailable'}${indexed ? ` · Last committed ${indexed}` : ' · Successful update time unavailable'}${source.github?.last_error ? ` · Latest refresh failed: ${source.github.last_error}` : ''}`;
  }
  if (source.kind !== 'filesystem') return `Index status unavailable for this source type · ${docs}`;
  const indexed = time(watcher?.last_success_at || source.last_indexed_at);
  const progress = watcher?.progress;
  const attempt = watcher?.state === 'starting' ? 'Refresh is starting.' :
    watcher?.state === 'indexing' ? `Refresh in progress${progress?.attempt_id ? ` · ${progress.phase || 'working'} · ${progress.processed_documents || 0} processed · ${progress.skipped_entries || 0} skipped` : ''}; the last committed snapshot remains searchable.` :
    watcher?.state === 'error' ? `Latest refresh failed${watcher.last_error ? `: ${watcher.last_error}` : '.'} The last committed snapshot remains searchable.` :
    watcher?.progress?.failure_code ? `Latest attempt did not commit (${watcher.progress.failure_code}); the last committed snapshot remains searchable.` :
    watcher?.progress?.attempt_id && watcher.progress.committed ? 'Latest refresh committed.' : '';
  const watch = watcher?.watch_warning ? `Automatic watcher degraded; using ${watcher.mode || 'fallback'} refresh.` : watcher?.mode === 'polling' ? 'Automatic polling refresh.' : '';
  return `${docs} · ${indexed ? `Last successful update ${indexed}` : 'No successful update time available'}${attempt ? ` · ${attempt}` : ''}${watch ? ` · ${watch}` : ''}${watcher?.last_error && watcher.state !== 'error' ? ` · ${watcher.last_error}` : ''}`;
}

export function sourceHealth(sources, sync) {
  if (!Array.isArray(sources) || !sync || !Array.isArray(sync.sources)) return { state: 'unknown', text: 'Source health unavailable. Search uses the last loaded index; retry to check.' };
  const fs = sources.filter(source => source.kind === 'filesystem');
  const states = new Map(sync.sources.map(item => [item.source_id, item]));
  const failed = fs.filter(source => {
    const watcher = states.get(source.id);
    return watcher?.state === 'error' || watcher?.progress?.failure_code || watcher?.last_error_code;
  });
  const running = fs.filter(source => states.get(source.id)?.state === 'indexing');
  const degraded = fs.filter(source => states.get(source.id)?.watch_warning || states.get(source.id)?.mode === 'polling');
  const unknown = fs.filter(source => !['idle', 'indexing', 'error'].includes(states.get(source.id)?.state));
  const text = [
    sync.enabled === false ? 'Automatic folder refresh is off; this does not verify current files.' : 'Folder freshness is based on the last committed index; idle does not verify current files.',
    running.length ? `${running.length} folder refresh in progress; committed results remain available.` : '',
    failed.length ? `${failed.length} latest folder refresh attempt(s) did not commit; committed results remain available.` : '',
    degraded.length ? `${degraded.length} folder watcher(s) degraded or using polling.` : '',
    unknown.length ? `${unknown.length} folder status unavailable.` : '',
    sources.filter(source => source.kind === 'github').length ? 'GitHub sources are manually refreshed snapshots.' : '',
    sources.filter(source => source.kind === 'archive').length ? 'Archive sources are frozen snapshots.' : '',
  ].filter(Boolean).join('\n');
  return { state: failed.length || degraded.length || unknown.length || (sync.enabled === false && fs.length > 0) ? 'warning' : running.length ? 'busy' : 'known', text };
}
