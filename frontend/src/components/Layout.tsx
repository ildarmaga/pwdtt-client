import { useState } from 'react';
import { Outlet } from 'react-router-dom';
import Sidebar from './Sidebar';
import Settings from '../modals/Settings';
import DevMetricsPanel from './DevMetricsPanel';
import { isBrowserDev } from '../lib/dev/mockWails';

export default function Layout() {
  const [settingsOpen, setSettingsOpen] = useState(false);

  return (
    <div style={{ display: 'flex', height: isBrowserDev ? '100%' : '100svh', minHeight: 0, background: 'var(--bg)', boxSizing: 'border-box' }}>
      <Sidebar onSettings={() => setSettingsOpen(true)} />
      <div style={{ flex: 1, display: 'flex', flexDirection: 'column', overflow: 'hidden' }}>
        <Outlet />
      </div>
      {settingsOpen && <Settings onClose={() => setSettingsOpen(false)} />}
      <DevMetricsPanel />
    </div>
  );
}
