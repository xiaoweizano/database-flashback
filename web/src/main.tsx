import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { BrowserRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfigProvider } from 'antd';
import { LocaleProvider } from './hooks/useLocale';
import { useLocale } from './hooks/useLocale';
import App from './App';
import './index.css';

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: 1,
      refetchOnWindowFocus: false,
      staleTime: 30_000,
    },
  },
});

const theme = {
  token: {
    colorPrimary: '#6366f1',
    colorSuccess: '#22c55e',
    colorWarning: '#f59e0b',
    colorError: '#ef4444',
    colorInfo: '#6366f1',
    borderRadius: 8,
    fontFamily: '-apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, "Noto Sans", sans-serif',
    colorBgLayout: '#f0f2f5',
  },
  components: {
    Card: {
      borderRadius: 12,
      boxShadowTertiary: '0 1px 3px rgba(0,0,0,0.08)',
    },
    Table: {
      borderRadius: 8,
    },
    Button: {
      borderRadius: 8,
      primaryShadow: '0 2px 6px rgba(99,102,241,0.3)',
    },
    Input: {
      borderRadius: 8,
    },
    Menu: {
      borderRadius: 8,
    },
  },
};

function ThemedApp() {
  const { antdLocale } = useLocale();
  return (
    <ConfigProvider locale={antdLocale} theme={theme}>
      <BrowserRouter>
        <App />
      </BrowserRouter>
    </ConfigProvider>
  );
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <LocaleProvider>
        <ThemedApp />
      </LocaleProvider>
    </QueryClientProvider>
  </StrictMode>,
);
