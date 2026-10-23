import { create } from 'zustand';
import { persist } from 'zustand/middleware';
import type { ModelSelectionState } from '@/types/ui.types';

export const useModelStore = create<ModelSelectionState>()(
  persist(
    (set, get) => ({
      modelByChat: {},
      favoriteModelIds: [],
      selectModel: (chatId: string, modelId: string) => {
        const trimmedId = modelId?.trim();
        if (trimmedId && get().modelByChat[chatId] !== trimmedId) {
          set((state) => ({
            modelByChat: { ...state.modelByChat, [chatId]: trimmedId },
          }));
        }
      },
      toggleFavoriteModel: (modelId: string) => {
        set((state) => {
          const exists = state.favoriteModelIds.includes(modelId);
          return {
            favoriteModelIds: exists
              ? state.favoriteModelIds.filter((id) => id !== modelId)
              : [...state.favoriteModelIds, modelId],
          };
        });
      },
    }),
    { name: 'model-storage' },
  ),
);
