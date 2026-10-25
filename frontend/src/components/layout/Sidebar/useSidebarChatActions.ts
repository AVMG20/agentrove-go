import { useState, useRef, useCallback, type RefObject } from 'react';
import { useMountEffect } from '@/hooks/useMountEffect';
import type { NavigateFunction } from 'react-router-dom';
import toast from 'react-hot-toast';
import type { Chat } from '@/types/chat.types';
import {
  useDeleteChatMutation,
  useGenerateChatTitleMutation,
  useUpdateChatMutation,
  usePinChatMutation,
} from '@/hooks/queries/useChatQueries';
import { useUIStore } from '@/store/uiStore';
import { MAX_CHAT_PANES } from '@/types/ui.types';
import { useChatStore } from '@/store/chatStore';
import { mutateWithToast, calculateDropdownPosition } from './sidebarHelpers';

interface UseSidebarChatActionsParams {
  selectedChatId: string | null;
  isMobile: boolean;
  navigate: NavigateFunction;
  scrollContainerRef: RefObject<HTMLDivElement | null>;
}

export function useSidebarChatActions({
  selectedChatId,
  isMobile,
  navigate,
  scrollContainerRef,
}: UseSidebarChatActionsParams) {
  const [chatToDelete, setChatToDelete] = useState<string | null>(null);
  const [chatToRename, setChatToRename] = useState<Chat | null>(null);
  const [dropdown, setDropdown] = useState<{
    chat: Chat;
    position: { top: number; left: number };
  } | null>(null);

  const dropdownRef = useRef<HTMLDivElement>(null);
  const deleteChat = useDeleteChatMutation();
  const updateChat = useUpdateChatMutation();
  const generateChatTitle = useGenerateChatTitleMutation();
  const pinChat = usePinChatMutation();
  const splitChatIds = useUIStore((state) => state.splitChatIds);

  useMountEffect(() => {
    const handleClickOutside = (event: MouseEvent) => {
      if (dropdownRef.current && !dropdownRef.current.contains(event.target as Node)) {
        setDropdown(null);
      }
    };

    document.addEventListener('mousedown', handleClickOutside);
    return () => document.removeEventListener('mousedown', handleClickOutside);
  });

  const dropdownStateRef = useRef(dropdown);
  dropdownStateRef.current = dropdown;

  useMountEffect(() => {
    const scrollContainer = scrollContainerRef.current;
    if (!scrollContainer) return;

    const handleScroll = () => {
      if (dropdownStateRef.current) setDropdown(null);
    };

    scrollContainer.addEventListener('scroll', handleScroll, { passive: true });
    return () => scrollContainer.removeEventListener('scroll', handleScroll);
  });

  const onChatSelect = useCallback((chatId: string) => navigate(`/chat/${chatId}`), [navigate]);

  // A plain click opens just that thread: it closes any split, including when
  // the thread is already the main pane (otherwise the click did nothing).
  const handleChatSelect = useCallback(
    (chatId: string) => {
      useUIStore.getState().closeSplitChat();
      onChatSelect(chatId);
      if (isMobile) {
        useUIStore.getState().setSidebarOpen(false);
      }
    },
    [onChatSelect, isMobile],
  );

  const showSplitAffordance = !isMobile && selectedChatId != null;
  const canOpenChatInSplit = useCallback(
    (chatId: string) =>
      showSplitAffordance &&
      chatId !== selectedChatId &&
      (splitChatIds.includes(chatId) || splitChatIds.length < MAX_CHAT_PANES - 1),
    [selectedChatId, showSplitAffordance, splitChatIds],
  );

  const handleOpenInSplit = useCallback(
    (chatId: string) => {
      if (!canOpenChatInSplit(chatId)) {
        onChatSelect(chatId);
        return;
      }
      useUIStore.getState().openChatInSplit(chatId);
    },
    [canOpenChatInSplit, onChatSelect],
  );

  const handleDropdownOpenInSplit = useCallback(
    (chatId: string) => {
      handleOpenInSplit(chatId);
      setDropdown(null);
    },
    [handleOpenInSplit],
  );
  const dropdownShowSplit = showSplitAffordance && dropdown?.chat.id !== selectedChatId;
  const dropdownCanSplit = !!dropdown?.chat.id && canOpenChatInSplit(dropdown.chat.id);

  const handleDeleteChat = useCallback((chatId: string) => {
    setChatToDelete(chatId);
    setDropdown(null);
  }, []);

  const confirmDeleteChat = useCallback(async () => {
    if (!chatToDelete) return;
    try {
      await mutateWithToast(
        () => deleteChat.mutateAsync(chatToDelete),
        'Chat deleted successfully',
        'Failed to delete chat',
      );
      const uiState = useUIStore.getState();
      if (chatToDelete === selectedChatId) {
        uiState.closeSplitChat();
      } else if (uiState.splitChatIds.includes(chatToDelete)) {
        uiState.closeSplitChat(chatToDelete);
      }
      if (chatToDelete === selectedChatId) {
        navigate('/');
      }
      // Release any pending File blobs held for this chat.
      useChatStore.getState().clearAttachedFilesForChat(chatToDelete);
    } catch {
      // toast already shown by mutateWithToast
    } finally {
      setChatToDelete(null);
    }
  }, [chatToDelete, deleteChat, selectedChatId, navigate]);

  const handleDropdownClick = useCallback((e: React.MouseEvent<HTMLButtonElement>, chat: Chat) => {
    e.stopPropagation();
    const rect = e.currentTarget.getBoundingClientRect();

    setDropdown((prev) => {
      if (prev?.chat.id === chat.id) {
        return null;
      }

      const position = calculateDropdownPosition(rect);
      return { chat, position };
    });
  }, []);

  const handleRenameClick = useCallback((chat: Chat) => {
    setChatToRename(chat);
    setDropdown(null);
  }, []);

  const handleSaveRename = useCallback(
    async (newTitle: string) => {
      if (!chatToRename) return;
      try {
        await mutateWithToast(
          () =>
            updateChat.mutateAsync({ chatId: chatToRename.id, updateData: { title: newTitle } }),
          'Chat renamed successfully',
          'Failed to rename chat',
        );
      } catch {
        // toast already shown by mutateWithToast
      } finally {
        setChatToRename(null);
      }
    },
    [chatToRename, updateChat],
  );

  const handleGenerateChatTitle = useCallback(async () => {
    if (!chatToRename) return '';
    try {
      return await generateChatTitle.mutateAsync(chatToRename.id);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : 'Failed to generate title');
      return '';
    }
  }, [chatToRename, generateChatTitle]);

  const handleTogglePin = useCallback(
    async (chat: Chat) => {
      setDropdown(null);
      const isPinned = !!chat.pinned_at;
      try {
        await mutateWithToast(
          () => pinChat.mutateAsync({ chatId: chat.id, pinned: !isPinned }),
          isPinned ? 'Chat unpinned' : 'Chat pinned',
          'Failed to update pin status',
        );
      } catch {
        // toast already shown by mutateWithToast
      }
    },
    [pinChat],
  );

  return {
    dropdown,
    setDropdown,
    dropdownRef,
    chatToDelete,
    setChatToDelete,
    chatToRename,
    setChatToRename,
    showSplitAffordance,
    canOpenChatInSplit,
    dropdownShowSplit,
    dropdownCanSplit,
    updateChat,
    generateChatTitle,
    handleChatSelect,
    handleOpenInSplit,
    handleDropdownOpenInSplit,
    handleDeleteChat,
    confirmDeleteChat,
    handleDropdownClick,
    handleRenameClick,
    handleSaveRename,
    handleGenerateChatTitle,
    handleTogglePin,
  };
}

export type SidebarChatActions = ReturnType<typeof useSidebarChatActions>;
