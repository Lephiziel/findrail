function isEditable(target) {
  for (let node = target; node; node = node.parentElement) {
    if (node.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(node.tagName)) return true;
  }
  return false;
}

export function isSearchShortcut(event, document) {
  if (event.defaultPrevented || event.isComposing || event.keyCode === 229 || isEditable(event.target)) return false;
  if ([...document.querySelectorAll('dialog')].some(dialog => dialog.open)) return false;
  const key = event.key.toLowerCase();
  const commandK = key === 'k' && (event.ctrlKey || event.metaKey) && !(event.ctrlKey && event.metaKey) && !event.altKey && !event.shiftKey;
  const slash = event.key === '/' && !event.ctrlKey && !event.metaKey && !event.altKey && !event.shiftKey;
  return commandK || slash;
}

export function installSearchShortcut(document, search) {
  const handler = event => {
    if (!isSearchShortcut(event, document)) return;
    event.preventDefault();
    search.focus();
  };
  document.addEventListener('keydown', handler);
  return () => document.removeEventListener('keydown', handler);
}

if (typeof document !== 'undefined') {
  const install = () => {
    const search = document.getElementById('query');
    if (search) installSearchShortcut(document, search);
  };
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', install, { once: true });
  else install();
}
