import { useState } from 'react';
import { Outlet, useLocation } from 'react-router-dom';
import Sidebar from './Sidebar';
import Deploy from '../modals/Deploy';
import Settings from '../modals/Settings';
import DevMetricsPanel from './DevMetricsPanel';
import { isBrowserDev } from '../lib/dev/mockWails';

export default function Layout() {
  const [deployOpen, setDeployOpen] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const { pathname } = useLocation();

  return (
    <div style={{ display: 'flex', height: '100vh', background: 'var(--bg)', boxSizing: 'border-box' }}>
      {isBrowserDev && (
        <div style={{
          position: 'fixed', top: 0, left: 0, right: 0, zIndex: 9999,
          background: '#7c3aed', color: '#fff', fontSize: 11, fontWeight: 600,
          textAlign: 'center', padding: '4px 8px', pointerEvents: 'none',
        }}>
          DEV preview — sub/подключение/метрики симулируются (см. панель справа внизу)
        </div>
      )}
      <Sidebar
        onDeploy={() => setDeployOpen(o => !o)}
        onSettings={() => setSettingsOpen(true)}
        deployActive={deployOpen}
        pathname={pathname}
      />
      <div style={{ flex: 1, display: 'flex', flexDirection: 'column', overflow: 'hidden' }}>
        <Outlet />
      </div>
      {deployOpen && <Deploy onClose={() => setDeployOpen(false)} />}
      {settingsOpen && <Settings onClose={() => setSettingsOpen(false)} />}
      <DevMetricsPanel />
    </div>
  );
}
