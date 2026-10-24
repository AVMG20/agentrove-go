import { useNavigate } from 'react-router-dom';
import { useMountEffect } from '@/hooks/useMountEffect';
import { useUIStore } from '@/store/uiStore';
import { useChatStore } from '@/store/chatStore';
import { useQueryClient } from '@tanstack/react-query';
import {
  COMMAND_MENU_SHORTCUT_CODE,
  SHORTCUT_MAP,
  executeCommand,
  resolveActiveGitTarget,
} from '@/components/ui/command-menu/commandRegistry';
import { MOBILE_BREAKPOINT } from '@/config/constants';

function isEmbeddedEditor(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  return !!target.closest('.monaco-editor, .xterm');
}

export function useCommandMenu() {
  const queryClient = useQueryClient();
  const navigate = useNavigate();

  useMountEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (!(e.ctrlKey || e.metaKey) || !e.shiftKey) return;

      // Skip Monaco/xterm — they own their own command palette.
      if (e.code === COMMAND_MENU_SHORTCUT_CODE) {
        if (isEmbeddedEditor(e.target)) return;
        e.preventDefault();
        const { commandMenuOpen, setCommandMenuOpen } = useUIStore.getState();
        setCommandMenuOpen(!commandMenuOpen);
        return;
      }

      if (isEmbeddedEditor(e.target)) return;
      if (useUIStore.getState().commandMenuOpen) return;

      const cmd = SHORTCUT_MAP.get(e.code);
      if (!cmd) return;

      if (cmd.hideOnMobile && window.innerWidth < MOBILE_BREAKPOINT) return;
      if (cmd.requiresChat && !useChatStore.getState().currentChat) return;
      const gitTarget = resolveActiveGitTarget(queryClient);
      if (cmd.requiresSandbox && !gitTarget.sandboxId) return;

      e.preventDefault();
      executeCommand(cmd, queryClient, navigate, true, gitTarget);
    };

    window.addEventListener('keydown', handleKeyDown, { capture: true });
    return () => window.removeEventListener('keydown', handleKeyDown, { capture: true });
  });
}
