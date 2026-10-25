import { create } from 'zustand';
import { persist } from 'zustand/middleware';

interface ProfileSelectionState {
  // The profile last picked in a composer; new threads start on it.
  lastProfileId: string | null;
  setLastProfileId: (profileId: string | null) => void;
}

export const useProfileStore = create<ProfileSelectionState>()(
  persist(
    (set) => ({
      lastProfileId: null,
      setLastProfileId: (profileId) => set({ lastProfileId: profileId }),
    }),
    { name: 'profile-storage' },
  ),
);
