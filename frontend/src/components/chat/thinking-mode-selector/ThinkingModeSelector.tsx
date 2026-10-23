import { memo } from 'react';
import clsx from 'clsx';
import { Brain } from 'lucide-react';
import { Dropdown, type DropdownPosition } from '@/components/ui/primitives/Dropdown/Dropdown';
import {
  useChatSettingsStore,
  DEFAULT_CHAT_SETTINGS_KEY,
  DEFAULT_THINKING_MODE,
} from '@/store/chatSettingsStore';
import { useIsSplitMode } from '@/hooks/useIsSplitMode';
import type { Model } from '@/types/chat.types';
import { getThinkingModeOption, getThinkingModes } from './thinkingModes';
import styles from './ThinkingModeSelector.module.scss';

interface ThinkingModeControlProps {
  model?: Model;
  dropdownPosition?: DropdownPosition;
  disabled?: boolean;
  variant?: 'default' | 'text';
  dropdownAlign?: 'left' | 'right';
}

export interface ThinkingModeDropdownProps extends ThinkingModeControlProps {
  value: string;
  onChange: (mode: string) => void;
}

export const ThinkingModeDropdown = memo(function ThinkingModeDropdown({
  value,
  onChange,
  model,
  dropdownPosition = 'bottom',
  dropdownAlign,
  disabled = false,
  variant = 'default',
}: ThinkingModeDropdownProps) {
  const isSplitMode = useIsSplitMode();

  const modes = getThinkingModes(model);
  const selectedMode = getThinkingModeOption(value, model);

  // Models without reasoning tiers (e.g. Haiku) hide the selector entirely.
  if (!selectedMode || modes.length === 0) return null;

  return (
    <Dropdown
      value={selectedMode}
      items={modes}
      getItemKey={(mode) => mode.value}
      getItemLabel={(mode) => mode.label}
      onSelect={(mode) => onChange(mode.value)}
      leftIcon={Brain}
      width="8rem"
      dropdownPosition={dropdownPosition}
      disabled={disabled}
      compactOnMobile
      forceCompact={isSplitMode}
      triggerVariant={variant}
      dropdownAlign={dropdownAlign}
      renderItem={(mode, isSelected) => (
        <span className={clsx(styles['mode-label'], isSelected && styles['mode-label--selected'])}>
          {mode.label}
        </span>
      )}
    />
  );
});

export interface ThinkingModeSelectorProps extends ThinkingModeControlProps {
  chatId?: string;
}

export const ThinkingModeSelector = memo(function ThinkingModeSelector({
  chatId,
  ...controlProps
}: ThinkingModeSelectorProps) {
  const key = chatId ?? DEFAULT_CHAT_SETTINGS_KEY;
  const thinkingMode = useChatSettingsStore(
    (state) => state.thinkingModeByChat[key] ?? DEFAULT_THINKING_MODE,
  );

  return (
    <ThinkingModeDropdown
      value={thinkingMode}
      onChange={(mode) => useChatSettingsStore.getState().setThinkingMode(key, mode)}
      {...controlProps}
    />
  );
});
