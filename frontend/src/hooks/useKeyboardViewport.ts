import { useEffect } from 'react';

// The on-screen keyboard is taller than any browser toolbar change.
const KEYBOARD_MIN_HEIGHT = 120;

// iOS Safari, and Android Chrome since 108, don't shrink the layout (100dvh
// included) for the on-screen keyboard: they shrink the visual viewport and pan
// it, pushing the title bar off screen and leaving the composer wherever the pan
// lands. While the keyboard is up, pin the app shell to the visual viewport:
// html[data-keyboard-open] plus --app-height / --app-offset-top
// (Layout.module.scss), and --keyboard-inset for sheets pinned to the bottom
// edge. Browsers that resize the layout for the keyboard (Firefox Android,
// interactive-widget=resizes-content) never show the gap, so nothing changes.
export function useKeyboardViewport(enabled: boolean) {
  useEffect(() => {
    const viewport = window.visualViewport;
    if (!enabled || !viewport) return;
    const root = document.documentElement;
    let frame = 0;
    const clear = () => {
      root.removeAttribute('data-keyboard-open');
      for (const name of ['--app-height', '--app-offset-top', '--keyboard-inset']) {
        root.style.removeProperty(name);
      }
    };

    const apply = () => {
      frame = 0;
      // Scale-adjusted, so pinch zoom (which also shrinks the visual viewport) isn't
      // mistaken for a keyboard.
      const open = window.innerHeight - viewport.height * viewport.scale > KEYBOARD_MIN_HEIGHT;
      if (open) {
        root.setAttribute('data-keyboard-open', '');
        root.style.setProperty('--app-height', `${viewport.height}px`);
        root.style.setProperty('--app-offset-top', `${viewport.offsetTop}px`);
        const inset = window.innerHeight - viewport.height - viewport.offsetTop;
        root.style.setProperty('--keyboard-inset', `${Math.max(0, inset)}px`);
      } else {
        clear();
      }
    };
    const schedule = () => {
      if (!frame) frame = requestAnimationFrame(apply);
    };

    viewport.addEventListener('resize', schedule);
    viewport.addEventListener('scroll', schedule);
    apply();
    return () => {
      cancelAnimationFrame(frame);
      viewport.removeEventListener('resize', schedule);
      viewport.removeEventListener('scroll', schedule);
      clear();
    };
  }, [enabled]);
}
