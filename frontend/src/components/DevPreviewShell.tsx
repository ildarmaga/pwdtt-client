import { useEffect, useState, type ReactNode } from 'react';
import { isBrowserDev } from '../lib/dev/mockWails';
import { devWindowSizeStore } from '../lib/dev/devWindowSize';
import DevWindowSizePanel from './DevWindowSizePanel';

export default function DevPreviewShell({ children }: { children: ReactNode }) {
  const [size, setSize] = useState(() => devWindowSizeStore.get());

  useEffect(() => devWindowSizeStore.subscribe(setSize), []);

  if (!isBrowserDev) {
    return <>{children}</>;
  }

  return (
    <div style={{
      minHeight: '100svh',
      boxSizing: 'border-box',
      background: '#08080a',
      display: 'flex',
      alignItems: 'center',
      justifyContent: 'center',
      padding: '36px 16px 16px',
    }}>
      <div style={{
        position: 'fixed',
        top: 0,
        left: 0,
        right: 0,
        zIndex: 9999,
        background: '#7c3aed',
        color: '#fff',
        fontSize: 11,
        fontWeight: 600,
        textAlign: 'center',
        padding: '4px 8px',
        pointerEvents: 'none',
      }}>
        DEV preview — размер окна слева внизу, ошибки слева, метрики sub справа внизу
      </div>

      <div
        data-dev-window
        style={{
          width: size.width,
          height: size.height,
          maxWidth: 'calc(100vw - 32px)',
          maxHeight: 'calc(100svh - 52px)',
          flexShrink: 0,
          border: '1px solid #3f3f46',
          borderRadius: 8,
          overflow: 'hidden',
          boxShadow: '0 24px 80px rgba(0,0,0,0.65)',
          display: 'flex',
          flexDirection: 'column',
          background: 'var(--bg)',
        }}
      >
        <div style={{ flex: 1, minHeight: 0, display: 'flex', flexDirection: 'column' }}>
          {children}
        </div>
      </div>

      <DevWindowSizePanel />
    </div>
  );
}
