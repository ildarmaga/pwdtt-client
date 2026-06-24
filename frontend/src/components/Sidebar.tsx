import { useNavigate, useLocation } from 'react-router-dom';
import {
  IconPlugConnected,
  IconTerminal2,
  IconSettings2,
} from '@tabler/icons-react';
import { useMobileUI } from '../lib/useMobileUI';

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
        </div>
      </aside>
    </>
  );
}
