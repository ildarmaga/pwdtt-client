import { useNavigate, useLocation } from 'react-router-dom';
import {
  IconPlugConnected,
  IconTerminal2,
  IconSettings2,
} from '@tabler/icons-react';

const NAV = [
  { path: '/', icon: <IconPlugConnected stroke={2} size={22} /> },
  { path: '/logs', icon: <IconTerminal2 stroke={2} size={22} /> },
];

interface Props {
  onSettings?: () => void;
  pathname?: string;
}

export default function Sidebar({ onSettings, pathname: pathnameProp }: Props) {
  const navigate = useNavigate();
  const location = useLocation();
  const pathname = pathnameProp ?? location.pathname;

  return (
    <>
      <style>{`
        .sidebar { width: 70px; background: linear-gradient(to bottom, var(--sidebar-from), var(--sidebar-to)); border-radius: 12px; margin: 2px; display: flex; flex-direction: column; justify-content: space-between; padding: 16px 0; overflow: hidden; flex-shrink: 0; }
        .sidebar-top, .sidebar-bottom { display: flex; flex-direction: column; align-items: center; gap: 8px; }
        .nav-btn { width: 48px; height: 48px; border: none; border-radius: 12px; background: transparent; color: #fff; cursor: pointer; display: flex; align-items: center; justify-content: center; opacity: 0.75; transition: opacity 0.15s; }
        .nav-btn:hover { opacity: 1; }
        .nav-btn--active { background: var(--sidebar-btn-active); opacity: 1; border-radius: 16px 16px 16px 2px; }
      `}</style>
      <aside className="sidebar">
        <div className="sidebar-top">
          {NAV.map(({ path, icon }) => (
            <button
              key={path}
              className={`nav-btn${pathname === path ? ' nav-btn--active' : ''}`}
              onClick={() => navigate(path)}
            >
              {icon}
            </button>
          ))}
        </div>
        <div className="sidebar-bottom">
          <button className="nav-btn" onClick={onSettings}>
            <IconSettings2 stroke={2} size={22} />
          </button>
        </div>
      </aside>
    </>
  );
}
