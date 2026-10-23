import { DEFAULT_THINKING_MODE } from '@/store/chatSettingsStore';
import type { Model } from '@/types/chat.types';

export interface ThinkingModeOption {
  value: string;
  label: string;
}

const THINKING_MODE_ORDER = ['none', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max', 'ultra'];

const THINKING_MODE_LABELS: Record<string, string> = {
  none: 'None',
  minimal: 'Minimal',
  low: 'Low',
  medium: 'Medium',
  high: 'High',
  xhigh: 'XHigh',
  max: 'Max',
  ultra: 'Ultra',
};

const EMPTY_THINKING_MODES: ThinkingModeOption[] = [];

// Per-model tiers come from the models API (backend MODELS); an empty list
// hides the selector.
export function getThinkingModes(model: Model | undefined): ThinkingModeOption[] {
  if (!model?.thinking_modes.length) return EMPTY_THINKING_MODES;
  return model.thinking_modes.map((value) => ({
    value,
    label: THINKING_MODE_LABELS[value],
  }));
}

export function coerceThinkingMode(thinkingMode: string, model: Model | undefined): string {
  const modes = getThinkingModes(model);
  const requestedMode = THINKING_MODE_ORDER.includes(thinkingMode)
    ? thinkingMode
    : DEFAULT_THINKING_MODE;
  const exactMode = modes.find((mode) => mode.value === requestedMode);
  if (exactMode) return exactMode.value;

  const requestedIndex = THINKING_MODE_ORDER.indexOf(requestedMode);
  for (let index = requestedIndex - 1; index >= 0; index -= 1) {
    const lowerMode = modes.find((mode) => mode.value === THINKING_MODE_ORDER[index]);
    if (lowerMode) return lowerMode.value;
  }

  return modes[0]?.value ?? DEFAULT_THINKING_MODE;
}

export function getThinkingModeOption(
  thinkingMode: string,
  model: Model | undefined,
): ThinkingModeOption | null {
  const modes = getThinkingModes(model);
  if (modes.length === 0) return null;
  const effectiveMode = coerceThinkingMode(thinkingMode, model);
  const selectedMode = modes.find((mode) => mode.value === effectiveMode);

  if (!selectedMode) {
    throw new Error(`Missing thinking mode option: ${effectiveMode}`);
  }

  return selectedMode;
}
