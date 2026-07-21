import { useState, useEffect, useRef } from 'react';
import { Outlet, useLocation } from 'react-router-dom';
import Sidebar from './Sidebar';
import Settings from '../modals/Settings';
import VKAuth from '../modals/VKAuth';
import DevMetricsPanel from './DevMetricsPanel';
import DevConnectionErrorsPanel from './DevConnectionErrorsPanel';
import UpdateBanner from './UpdateBanner';
import { isBrowserDev } from '../lib/dev/mockWails';
import { settingsModalStore } from '../lib/stores/settingsModalStore';
import { vkModeStore } from '../lib/stores/vkModeStore';
import { SetVKUseCookies } from '../../wailsjs/go/backend/App';

export default function Layout() {
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [vkAuthOpen, setVkAuthOpen] = useState(false);
  const location = useLocation();
  const contentRef = useRef<HTMLDivElement>(null);

  useEffect(() => settingsModalStore.subscribe(() => setSettingsOpen(true)), []);

  // Форсируем перерисовку WebView2 после смены роута. Без этого композитор
  // иногда не инвалидирует старый кадр и оставлял «артефакты» элементов
  // прошлой страницы (помогало только перемещение окна). Промоут в отдельный
  // слой через translateZ + reflow в следующем кадре заставляет перекомпоновать.
  useEffect(() => {
    const el = contentRef.current;
    if (!el) return;
    el.style.transform = 'translateZ(0)';
    el.style.willChange = 'transform';
    let raf2 = 0;
    const raf1 = requestAnimationFrame(() => {
      void el.offsetHeight; // принудительный reflow
      raf2 = requestAnimationFrame(() => {
        el.style.transform = '';
        el.style.willChange = '';
      });
    });
    return () => {
      cancelAnimationFrame(raf1);
      cancelAnimationFrame(raf2);
    };
  }, [location.pathname]);

  return (
    <div style={{ display: 'flex', height: isBrowserDev ? '100%' : '100svh', minHeight: 0, background: 'var(--bg)', boxSizing: 'border-box' }}>
      <Sidebar
        onSettings={() => setSettingsOpen(true)}
        onVKLogin={() => setVkAuthOpen(true)}
      />
      <div ref={contentRef} style={{ flex: 1, display: 'flex', flexDirection: 'column', overflow: 'hidden' }}>
        <Outlet />
      </div>
      {settingsOpen && <Settings onClose={() => setSettingsOpen(false)} />}
      {vkAuthOpen && (
        <VKAuth
          onClose={() => setVkAuthOpen(false)}
          onDone={() => {
            void SetVKUseCookies(true).catch(() => {});
            vkModeStore.notify();
          }}
        />
      )}
      <UpdateBanner />
      <DevMetricsPanel />
      <DevConnectionErrorsPanel />
    </div>
  );
}
