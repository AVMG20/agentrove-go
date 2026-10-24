import type { KeyboardEvent } from 'react';
import { isCoarsePointer } from '@/utils/device';

// Touch keyboards have no Shift+Return, so on a coarse pointer (phones, tablets)
// Return inserts a newline and the send button sends. A hardware keyboard on
// such a device can still send with Cmd/Ctrl+Return.

export function isComposerSendKey(event: KeyboardEvent): boolean {
  if (event.key !== 'Enter' || event.shiftKey || event.nativeEvent?.isComposing) return false;
  return !isCoarsePointer() || event.metaKey || event.ctrlKey;
}

// Label for the on-screen keyboard's Return key, matching what it does.
export function composerEnterKeyHint(): 'enter' | 'send' {
  return isCoarsePointer() ? 'enter' : 'send';
}
