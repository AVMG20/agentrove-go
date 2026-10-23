import type { ToasterProps } from 'react-hot-toast';

// Global .app-toast (app.scss) — rht owns the DOM so CSS modules can't theme it.
const toastSurfaceClass = 'app-toast';

export const toasterConfig: ToasterProps = {
  position: 'top-right',
  // Below safe-area inset (+1rem gap). Does not clear TitleBar height on chat screens.
  containerStyle: {
    top: 'calc(env(safe-area-inset-top) + 1rem)',
  },
  toastOptions: {
    className: '',
    style: {
      padding: '10px 14px',
      fontSize: '14px',
      fontFamily: 'inherit',
      maxWidth: '380px',
    },
    duration: 4000,
    success: {
      iconTheme: {
        primary: '#22c55e',
        secondary: '#f0fdf4',
      },
      className: toastSurfaceClass,
    },
    error: {
      iconTheme: {
        primary: '#ef4444',
        secondary: '#fef2f2',
      },
      className: toastSurfaceClass,
    },
    loading: {
      iconTheme: {
        primary: '#3b82f6',
        secondary: '#eff6ff',
      },
      className: toastSurfaceClass,
    },
    blank: {
      className: toastSurfaceClass,
    },
  },
};
