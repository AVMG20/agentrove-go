import { create } from 'zustand';
import { persist } from 'zustand/middleware';
import { authService } from '@/services/authService';

interface AuthState {
  isAuthenticated: boolean;
  setAuthenticated: (isAuth: boolean) => void;
}

export const useAuthStore = create<AuthState>()(
  persist(
    (set) => ({
      isAuthenticated: !!authService.getToken(),
      setAuthenticated: (isAuth: boolean) => {
        set({ isAuthenticated: isAuth });
      },
    }),
    {
      name: 'auth-storage',
      partialize: (state) => ({
        isAuthenticated: state.isAuthenticated,
      }),
    },
  ),
);
