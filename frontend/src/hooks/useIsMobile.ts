import { useSyncExternalStore } from 'react';
import { readLayoutMode, subscribeLayoutMode } from '@/utils/device';

// Point-in-time read for non-render code paths; useIsMobile is the reactive form.
// Narrow windows and phones (either way up) get the phone layout; see
// utils/device.ts, which also sets the html attributes the CSS keys off.
export const isMobileViewport = (): boolean => readLayoutMode().mobile;

const getServerSnapshot = () => false;

export function useIsMobile(): boolean {
  return useSyncExternalStore(subscribeLayoutMode, isMobileViewport, getServerSnapshot);
}
