import { SettingsContext } from '@/contexts/SettingsContextDefinition';
import { createContextHook } from '@/hooks/createContextHook';

export const useSettingsContext = createContextHook(
  SettingsContext,
  'useSettingsContext',
  'SettingsProvider',
);
