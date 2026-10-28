import { useEffect, type RefObject } from 'react';
import { artifactService } from '@/services/artifactService';
import type { ArtifactView } from '@/types/artifact.types';

// Collect a burst of errors (a failing render loop) into one report.
const ERROR_FLUSH_MS = 1_000;
// Typing into a stored field shouldn't write on every keystroke.
const STATE_FLUSH_MS = 400;

type FrameMessage =
  | { agentrove: 'error'; message: string }
  | { agentrove: 'state:load' }
  | { agentrove: 'state:set'; key: string; value: unknown };

const isFrameMessage = (data: unknown): data is FrameMessage =>
  typeof data === 'object' &&
  data !== null &&
  typeof (data as { agentrove?: unknown }).agentrove === 'string';

// The sandboxed page talks to the viewer over postMessage: the injected error
// reporter sends script errors and Kit.store loads and saves state. Only the
// owner's viewer persists either; other viewers get in-memory state.
export function useArtifactBridge(
  frameRef: RefObject<HTMLIFrameElement | null>,
  view: ArtifactView | undefined,
) {
  const artifactId = view?.id;
  const version = view?.version;
  const isOwner = view?.is_owner ?? false;

  useEffect(() => {
    if (!artifactId || version === undefined) return;
    const errors = new Set<string>();
    let errorTimer: ReturnType<typeof setTimeout> | undefined;
    const pendingState = new Map<string, unknown>();
    let stateTimer: ReturnType<typeof setTimeout> | undefined;

    const flushErrors = () => {
      errorTimer = undefined;
      const messages = [...errors];
      errors.clear();
      if (messages.length > 0) {
        void artifactService.reportErrors(artifactId, version, messages).catch(() => {});
      }
    };
    const flushState = () => {
      clearTimeout(stateTimer);
      stateTimer = undefined;
      for (const [key, value] of pendingState) {
        void artifactService.setState(artifactId, key, value).catch(() => {});
      }
      pendingState.clear();
    };

    const reply = (target: Window, state: Record<string, unknown>, persistent: boolean) =>
      // The frame's origin is opaque ("null"), so it can't be targeted by name.
      target.postMessage({ agentrove: 'state:init', state, persistent }, '*');

    const onMessage = (event: MessageEvent) => {
      const frame = frameRef.current?.contentWindow;
      if (!frame || event.source !== frame || !isFrameMessage(event.data)) return;
      const data = event.data;
      switch (data.agentrove) {
        case 'error':
          if (!isOwner || typeof data.message !== 'string') return;
          errors.add(data.message);
          errorTimer ??= setTimeout(flushErrors, ERROR_FLUSH_MS);
          return;
        case 'state:load':
          if (!isOwner) {
            reply(frame, {}, false);
            return;
          }
          artifactService
            .getState(artifactId)
            .then((state) => reply(frame, state, true))
            .catch(() => reply(frame, {}, false));
          return;
        case 'state:set':
          if (!isOwner || typeof data.key !== 'string') return;
          pendingState.set(data.key, data.value);
          clearTimeout(stateTimer);
          stateTimer = setTimeout(flushState, STATE_FLUSH_MS);
          return;
      }
    };

    window.addEventListener('message', onMessage);
    window.addEventListener('pagehide', flushState);
    return () => {
      window.removeEventListener('message', onMessage);
      window.removeEventListener('pagehide', flushState);
      clearTimeout(errorTimer);
      clearTimeout(stateTimer);
      flushErrors();
      flushState();
    };
  }, [frameRef, artifactId, version, isOwner]);
}
