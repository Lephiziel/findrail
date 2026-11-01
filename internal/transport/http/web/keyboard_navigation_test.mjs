import test from 'node:test';
import assert from 'node:assert/strict';
import { isSearchShortcut } from './keyboard_navigation.mjs';

const doc = open => ({ querySelectorAll: () => open ? [{ open: true }] : [] });
const event = (key, extras = {}) => ({ key, target: { tagName: 'BODY' }, ...extras });

test('accepts slash and exactly Ctrl+K or Cmd+K outside dialogs', () => {
  for (const key of ['/', 'k']) {
    assert.equal(isSearchShortcut(event(key, key === 'k' ? { ctrlKey: true } : {}), doc(false)), true);
  }
  assert.equal(isSearchShortcut(event('k', { metaKey: true }), doc(false)), true);
  assert.equal(isSearchShortcut(event('/', { shiftKey: true }), doc(false)), false);
});

test('does not steal editing, composition, modified keys, or modal input', () => {
  for (const target of [
    { tagName: 'INPUT' }, { tagName: 'TEXTAREA' }, { tagName: 'SELECT' },
    { tagName: 'SPAN', isContentEditable: true },
  ]) assert.equal(isSearchShortcut(event('/', { target }), doc(false)), false);
  assert.equal(isSearchShortcut(event('/', { isComposing: true }), doc(false)), false);
  assert.equal(isSearchShortcut(event('/', { keyCode: 229 }), doc(false)), false);
  for (const extras of [{ altKey: true }, { shiftKey: true }, { ctrlKey: true, metaKey: true }, { ctrlKey: true, altKey: true }]) {
    assert.equal(isSearchShortcut(event('k', extras), doc(false)), false);
  }
  assert.equal(isSearchShortcut(event('/', { defaultPrevented: true }), doc(false)), false);
  assert.equal(isSearchShortcut(event('/', {}), doc(true)), false);
});
