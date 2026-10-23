import { type ReactNode, useMemo } from 'react';
import { SettingsContext, type SettingsContextValue } from './SettingsContextDefinition';
import type { UserSettings } from '@/types/user.types';

interface SettingsProviderProps {
  localSettings: UserSettings;
  setLocalSettings: React.Dispatch<React.SetStateAction<UserSettings>>;
  persistSettings: SettingsContextValue['persistSettings'];
  settings: UserSettings;
  children: ReactNode;
}

export function SettingsProvider({
  localSettings,
  setLocalSettings,
  persistSettings,
  settings,
  children,
}: SettingsProviderProps) {
  const value = useMemo<SettingsContextValue>(
    () => ({ localSettings, setLocalSettings, persistSettings, settings }),
    [localSettings, setLocalSettings, persistSettings, settings],
  );
  return <SettingsContext.Provider value={value}>{children}</SettingsContext.Provider>;
}
