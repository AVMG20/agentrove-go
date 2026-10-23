import { memo, useMemo } from 'react';
import clsx from 'clsx';
import { AtSign, MessageSquare } from 'lucide-react';
import { SuggestionPanel } from './SuggestionPanel';
import { MentionIcon } from './MentionIcon';
import type { MentionItem } from '@/types/ui.types';
import styles from './MentionSuggestionsPanel.module.scss';

const NO_MEMBERS: MentionItem[] = [];

interface MentionSuggestionsPanelProps {
  members?: MentionItem[];
  files: MentionItem[];
  chats: MentionItem[];
  highlightedIndex: number;
  onSelect: (item: MentionItem) => void;
}

function renderFile(file: MentionItem, isActive: boolean) {
  return (
    <>
      <MentionIcon name={file.name} className={styles['item-icon']} />
      <div className={styles['item-row']}>
        <span className={clsx(styles['item-name'], isActive && styles['item-name--active'])}>
          {file.name}
        </span>
        <span className={styles['item-path']}>{file.path}</span>
      </div>
    </>
  );
}

function renderMember(member: MentionItem, isActive: boolean) {
  return (
    <>
      <AtSign className={styles['item-icon']} />
      <div className={styles['item-row']}>
        <span className={clsx(styles['item-name'], isActive && styles['item-name--active'])}>
          {member.name}
        </span>
      </div>
    </>
  );
}

function renderChat(chat: MentionItem, isActive: boolean) {
  return (
    <>
      <MessageSquare className={styles['item-icon']} />
      <div className={styles['item-row']}>
        <span className={clsx(styles['item-name'], isActive && styles['item-name--active'])}>
          {chat.name}
        </span>
      </div>
    </>
  );
}

const mentionItemKey = (item: MentionItem) => item.path;

export const MentionSuggestionsPanel = memo(function MentionSuggestionsPanel({
  members = NO_MEMBERS,
  files,
  chats,
  highlightedIndex,
  onSelect,
}: MentionSuggestionsPanelProps) {
  const sections = useMemo(
    () => [
      {
        label: 'Members',
        items: members,
        itemKey: mentionItemKey,
        renderItem: renderMember,
      },
      {
        label: 'Files',
        items: files,
        itemKey: mentionItemKey,
        renderItem: renderFile,
      },
      {
        label: 'Chats',
        items: chats,
        itemKey: mentionItemKey,
        renderItem: renderChat,
      },
    ],
    [members, files, chats],
  );

  return (
    <SuggestionPanel sections={sections} highlightedIndex={highlightedIndex} onSelect={onSelect} />
  );
});
