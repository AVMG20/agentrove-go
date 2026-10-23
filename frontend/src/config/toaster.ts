import type { ToasterProps } from 'react-hot-toast';

// Global .app-toast (app.scss) — rht owns the DOM so CSS modules can't theme it.
const toastSurfaceClass = 'app-toast';

export const toasterConfig: ToasterProps = {
  // Bottom-center keeps toasts away from the header controls people click.
  position: 'bottom-center',
  containerStyle: {
    bottom: 'calc(env(safe-area-inset-bottom) + 1rem)',
  },
  toastOptions: {
    className: '',
    style: {
      padding: '10px 14px',
      fontSize: '14px',
      fontFamily: 'inherit',
      maxWidth: '380px',
    },
    duration: 2500,
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
