import { useState, useEffect } from 'react';
import { useNavigate, useLocation } from 'react-router-dom';
import {
  IconPlugConnected,
  IconTerminal2,
  IconSettings2,
} from '@tabler/icons-react';
import { useMobileUI } from '../lib/useMobileUI';
import { serverStore } from '../lib/store';
import { selectedServerStore } from '../lib/stores/selectedServerStore';
import { GetProfile, GetAppVersion } from '../../wailsjs/go/backend/App';

function shortDeviceId(id: string): string {
  const s = id.trim();
  if (s.length <= 12) return s;
  return `${s.slice(0, 4)}…${s.slice(-4)}`;
}

const NAV = [
  { path: '/', icon: (s: number) => <IconPlugConnected stroke={2} size={s} /> },
  { path: '/logs', icon: (s: number) => <IconTerminal2 stroke={2} size={s} /> },
];

interface Props {
  onSettings?: () => void;
  pathname?: string;
}

export default function Sidebar({ onSettings, pathname: pathnameProp }: Props) {
  const navigate = useNavigate();
  const location = useLocation();
  const pathname = pathnameProp ?? location.pathname;
  const compact = useMobileUI();
  const [deviceId, setDeviceId] = useState('');
  const [appVersion, setAppVersion] = useState('');

  const refreshDeviceId = () => {
    const all = serverStore.getAll();
    const srv = all.find(s => s.id === selectedServerStore.getId()) ?? all[0];
    if (!srv) {
      setDeviceId('');
      return;
    }
    GetProfile(srv.id)
      .then(p => setDeviceId(p?.device_id?.trim() ?? ''))
      .catch(() => setDeviceId(''));
  };

  useEffect(() => {
    GetAppVersion().then(v => setAppVersion(v?.trim() ?? '')).catch(() => {});
    refreshDeviceId();
    const offSel = selectedServerStore.subscribe(refreshDeviceId);
    return () => { offSel(); };
  }, []);

  const copyDeviceId = () => {
    if (!deviceId) return;
    navigator.clipboard?.writeText(deviceId).catch(() => {});
  };

  return (
    <>
      <style>{`
        .sidebar { width: 70px; background: linear-gradient(to bottom, var(--sidebar-from), var(--sidebar-to)); border-radius: 12px; margin: 2px; display: flex; flex-direction: column; justify-content: space-between; padding: 16px 0; overflow: hidden; flex-shrink: 0; }
        .sidebar--compact { width: 52px; padding: 10px 0; border-radius: 10px; margin: 2px 2px 2px 0; }
        .sidebar-top, .sidebar-bottom { display: flex; flex-direction: column; align-items: center; gap: 8px; }
        .sidebar--compact .sidebar-top, .sidebar--compact .sidebar-bottom { gap: 6px; }
        .nav-btn { width: 48px; height: 48px; border: none; border-radius: 12px; background: transparent; color: #fff; cursor: pointer; display: flex; align-items: center; justify-content: center; opacity: 0.75; transition: opacity 0.15s; }
        .sidebar--compact .nav-btn { width: 38px; height: 38px; border-radius: 10px; }
        .nav-btn:hover { opacity: 1; }
        .nav-btn--active { background: var(--sidebar-btn-active); opacity: 1; border-radius: 16px 16px 16px 2px; }
        .sidebar--compact .nav-btn--active { border-radius: 12px 12px 12px 2px; }
        .sidebar-footer {
          display: flex; flex-direction: column; align-items: center; gap: 2px;
          padding: 6px 4px 10px; margin-top: 4px; min-width: 0; max-width: 100%;
          border-top: 1px solid color-mix(in srgb, #fff 8%, transparent);
        }
        .sidebar-footer-ver { font-size: 9px; font-weight: 600; color: color-mix(in srgb, #fff 45%, transparent); letter-spacing: 0.02em; line-height: 1.2; }
        .sidebar-footer-id {
          width: 100%; padding: 0; border: none; background: none; cursor: pointer;
          font-family: 'Geist Mono', ui-monospace, monospace; font-size: 8px; line-height: 1.25;
          color: color-mix(in srgb, #fff 38%, transparent); text-align: center;
          overflow: hidden; text-overflow: ellipsis; white-space: nowrap; direction: ltr;
        }
        .sidebar-footer-id:hover { color: color-mix(in srgb, #fff 70%, transparent); }
        .sidebar--compact .sidebar-footer { padding: 4px 2px 8px; }
        .sidebar--compact .sidebar-footer-ver { font-size: 8px; }
        .sidebar--compact .sidebar-footer-id { font-size: 7px; }
      `}</style>
      <aside className={`sidebar${compact ? ' sidebar--compact' : ''}`}>
        <div className="sidebar-top">
          {NAV.map(({ path, icon }) => (
            <button
              key={path}
              className={`nav-btn${pathname === path ? ' nav-btn--active' : ''}`}
              onClick={() => navigate(path)}
            >
              {icon(compact ? 18 : 22)}
            </button>
          ))}
        </div>
        <div className="sidebar-bottom">
          <button className="nav-btn" onClick={onSettings}>
            <IconSettings2 stroke={2} size={compact ? 18 : 22} />
          </button>
          {(appVersion || deviceId) && (
            <div className="sidebar-footer">
              {deviceId && (
                <button
                  type="button"
                  className="sidebar-footer-id"
                  title={`${deviceId}\nНажмите, чтобы скопировать`}
                  onClick={copyDeviceId}
                >
                  {shortDeviceId(deviceId)}
                </button>
              )}
              {appVersion && <span className="sidebar-footer-ver">v{appVersion.replace(/^v/i, '')}</span>}
            </div>
          )}
        </div>
      </aside>
    </>
  );
}
