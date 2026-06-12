import { useState, useEffect } from 'react';
import type React from 'react';
import {
  IconCloverFilled, IconPlus, IconChevronUp, IconPencil,
  IconFlameFilled, IconShieldFilled, IconLayoutGridFilled, IconCloudFilled, IconBrandSpeedtest,
  IconStarFilled, IconHeartFilled, IconBoltFilled, IconRocket,
  IconCrownFilled, IconDiamondFilled, IconLeafFilled, IconSnowflake,
  IconServer, IconGlobe, IconLockFilled, IconWifi, IconBrandTelegram,
} from '@tabler/icons-react';

const SERVER_ICONS: { key: string; render: (size: number) => React.ReactNode }[] = [
  { key: 'clover',     render: s => <IconCloverFilled size={s} /> },
  { key: 'flame',      render: s => <IconFlameFilled size={s} /> },
  { key: 'shield',     render: s => <IconShieldFilled size={s} /> },
  { key: 'grid',       render: s => <IconLayoutGridFilled size={s} /> },
  { key: 'cloud',      render: s => <IconCloudFilled size={s} /> },
  { key: 'speed',      render: s => <IconBrandSpeedtest size={s} stroke={2} /> },
  { key: 'star',       render: s => <IconStarFilled size={s} /> },
  { key: 'heart',      render: s => <IconHeartFilled size={s} /> },
  { key: 'bolt',       render: s => <IconBoltFilled size={s} /> },
  { key: 'rocket',     render: s => <IconRocket size={s} stroke={2} /> },
  { key: 'crown',      render: s => <IconCrownFilled size={s} /> },
  { key: 'diamond',    render: s => <IconDiamondFilled size={s} /> },
  { key: 'leaf',       render: s => <IconLeafFilled size={s} /> },
  { key: 'snowflake',  render: s => <IconSnowflake size={s} stroke={2} /> },
  { key: 'server',     render: s => <IconServer size={s} stroke={2} /> },
  { key: 'globe',      render: s => <IconGlobe size={s} stroke={2} /> },
  { key: 'lock',       render: s => <IconLockFilled size={s} /> },
  { key: 'wifi',       render: s => <IconWifi size={s} stroke={2} /> },
  { key: 'flag-ru',    render: s => <img src="/flags/ru.svg" width={s} height={s * 0.67} style={{ objectFit: 'cover', borderRadius: 2 }} /> },
  { key: 'flag-us',    render: s => <img src="/flags/us.svg" width={s} height={s * 0.67} style={{ objectFit: 'cover', borderRadius: 2 }} /> },
  { key: 'flag-de',    render: s => <img src="/flags/de.svg" width={s} height={s * 0.67} style={{ objectFit: 'cover', borderRadius: 2 }} /> },
  { key: 'flag-nl',    render: s => <img src="/flags/nl.svg" width={s} height={s * 0.67} style={{ objectFit: 'cover', borderRadius: 2 }} /> },
  { key: 'flag-fi',    render: s => <img src="/flags/fi.svg" width={s} height={s * 0.67} style={{ objectFit: 'cover', borderRadius: 2 }} /> },
  { key: 'flag-fr',    render: s => <img src="/flags/fr.svg" width={s} height={s * 0.67} style={{ objectFit: 'cover', borderRadius: 2 }} /> },
  { key: 'flag-gb',    render: s => <img src="/flags/gb.svg" width={s} height={s * 0.67} style={{ objectFit: 'cover', borderRadius: 2 }} /> },
  { key: 'flag-jp',    render: s => <img src="/flags/jp.svg" width={s} height={s * 0.67} style={{ objectFit: 'cover', borderRadius: 2 }} /> },
  { key: 'flag-pl',    render: s => <img src="/flags/pl.svg" width={s} height={s * 0.67} style={{ objectFit: 'cover', borderRadius: 2 }} /> },
  { key: 'flag-se',    render: s => <img src="/flags/se.svg" width={s} height={s * 0.67} style={{ objectFit: 'cover', borderRadius: 2 }} /> },
  { key: 'flag-ch',    render: s => <img src="/flags/ch.svg" width={s} height={s * 0.67} style={{ objectFit: 'cover', borderRadius: 2 }} /> },
  { key: 'flag-lt',    render: s => <img src="/flags/lt.svg" width={s} height={s * 0.67} style={{ objectFit: 'cover', borderRadius: 2 }} /> },
  { key: 'flag-lv',    render: s => <img src="/flags/lv.svg" width={s} height={s * 0.67} style={{ objectFit: 'cover', borderRadius: 2 }} /> },
  { key: 'flag-ee',    render: s => <img src="/flags/ee.svg" width={s} height={s * 0.67} style={{ objectFit: 'cover', borderRadius: 2 }} /> },
  { key: 'flag-cz',    render: s => <img src="/flags/cz.svg" width={s} height={s * 0.67} style={{ objectFit: 'cover', borderRadius: 2 }} /> },
  { key: 'flag-at',    render: s => <img src="/flags/at.svg" width={s} height={s * 0.67} style={{ objectFit: 'cover', borderRadius: 2 }} /> },
  { key: 'flag-ca',    render: s => <img src="/flags/ca.svg" width={s} height={s * 0.67} style={{ objectFit: 'cover', borderRadius: 2 }} /> },
  { key: 'flag-au',    render: s => <img src="/flags/au.svg" width={s} height={s * 0.67} style={{ objectFit: 'cover', borderRadius: 2 }} /> },
  { key: 'flag-sg',    render: s => <img src="/flags/sg.svg" width={s} height={s * 0.67} style={{ objectFit: 'cover', borderRadius: 2 }} /> },
  { key: 'flag-hk',    render: s => <img src="/flags/hk.svg" width={s} height={s * 0.67} style={{ objectFit: 'cover', borderRadius: 2 }} /> },
  { key: 'flag-tr',    render: s => <img src="/flags/tr.svg" width={s} height={s * 0.67} style={{ objectFit: 'cover', borderRadius: 2 }} /> },
  { key: 'flag-kz',    render: s => <img src="/flags/kz.svg" width={s} height={s * 0.67} style={{ objectFit: 'cover', borderRadius: 2 }} /> },
];

function ServerIcon({ iconKey, size }: { iconKey?: string; size: number }) {
  const entry = SERVER_ICONS.find(i => i.key === (iconKey ?? 'clover')) ?? SERVER_ICONS[0];
  return <>{entry.render(size)}</>;
}
import AddServer from '../modals/Add-server';
import EditServer from '../modals/Edit-server';
import { serverStore, settingsStore } from '../lib/store';
import { tunnelStore } from '../lib/stores/tunnelStore';
import { toastStore } from '../lib/stores/toastStore';
import { wdttLinkStore, fetchTrafficStats, formatBytes, trafficRemainLabel, trafficCompactLabel, trafficUsedPercent, trafficFillColor, expireLabel, subRefreshMs, type TrafficStats } from '../lib/utils/wdttLink';
import { SaveProfile } from '../../wailsjs/go/backend/App';
import type { Server, TunnelState } from '../lib/types';
import { Connect as WailsConnect, Disconnect as WailsDisconnect } from '../../wailsjs/go/backend/App';
import shapeDark from '../assets/shape-dark.png';
import powerIcon from '../assets/power-icon.png';

const PING_COLORS: Record<string, string> = {
  good: '#22c55e',
  mid: '#f59e0b',
  bad: '#ef4444',
  none: 'var(--border)',
};

function pingColor(ping?: number) {
  if (!ping) return PING_COLORS.none;
  if (ping < 100) return PING_COLORS.good;
  if (ping < 200) return PING_COLORS.mid;
  return PING_COLORS.bad;
}

const TUNNEL_LABEL: Record<TunnelState, string> = {
  idle: 'Подключить',
  connecting: 'Подключение...',
  connected: 'Отключить',
  disconnecting: 'Отключение...',
};

export default function Connect() {
  const [servers, setServers] = useState<Server[]>(() => serverStore.getAll());
  const [selected, setSelected] = useState<Server | null>(() => {
    const all = serverStore.getAll();
    return all.length > 0 ? all[0] : null;
  });
  const [listOpen, setListOpen] = useState(false);

  // tunnelState из глобального store — переживает смену роута
  const [tunnelState, setTunnelState] = useState<TunnelState>(() => tunnelStore.get());
  useEffect(() => tunnelStore.subscribe(setTunnelState), []);

  const [addServerOpen, setAddServerOpen] = useState(false);
  const [editServer, setEditServer] = useState<Server | null>(null);
  const [linkFlash, setLinkFlash] = useState(false);
  const [traffic, setTraffic] = useState<TrafficStats | null>(null);

  useEffect(() => {
    return wdttLinkStore.subscribe((link) => {
      if (!link) return;
      const consumed = wdttLinkStore.consume();
      if (!consumed) return;
      const host = `${consumed.ip}:${consumed.dtlsPort}`;
      const name = consumed.name;

      const applyLink = async () => {
        const h4 = consumed.hashes.slice(0, 4);
        const padded: [string,string,string,string] = [h4[0]??'', h4[1]??'', h4[2]??'', h4[3]??''];
        await SaveProfile(name, {
          peer: host,
          password: consumed.password,
          hashes: [],
          turn: '', port: '', device_id: '', listen: '',
        });
        const existing = serverStore.getAll().find(s => s.host === host);
        let s = existing ?? serverStore.add({
          name,
          host,
          password: consumed.password,
          subUrl: consumed.subUrl,
        });
        if (consumed.hashes.length > 0 || consumed.subUrl) {
          const updated = {
            ...s,
            hashes: padded,
            subUrl: consumed.subUrl ?? s.subUrl,
          };
          serverStore.update(updated);
          s = updated;
        }
        if (consumed.stats) {
          setTraffic(consumed.stats);
          if (consumed.stats.announce) {
            toastStore.show(consumed.stats.announce, 6000);
          }
        }
        setServers(serverStore.getAll());
        setSelected({ ...s });
        setLinkFlash(true);
        setTimeout(() => setLinkFlash(false), 800);
        toastStore.show(existing ? `Профиль обновлён: ${name}` : `Профиль добавлен: ${name}`, 3000);
      };
      applyLink();
    });
  }, []);

  useEffect(() => {
    if (!selected?.subUrl) {
      setTraffic(null);
      return;
    }
    let cancelled = false;
    let timer = 0;
    const schedule = (ms: number) => {
      window.clearInterval(timer);
      timer = window.setInterval(refresh, ms);
    };
    const refresh = async () => {
      const stats = await fetchTrafficStats(selected.subUrl!);
      if (cancelled || !stats) return;
      setTraffic(stats);
      schedule(subRefreshMs(stats));
    };
    refresh();
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [selected?.subUrl, selected?.id]);

  useEffect(() => {
    if (tunnelState !== 'connected' || !selected?.subUrl) return;
    let cancelled = false;
    fetchTrafficStats(selected.subUrl).then((stats) => {
      if (!cancelled && stats) setTraffic(stats);
    });
    return () => { cancelled = true; };
  }, [tunnelState, selected?.subUrl, selected?.id]);

  const doConnect = async () => {
    const s = settingsStore.get();
    const hashes = (s.useGlobalHashes
      ? s.hashes
      : (selected!.hashes ?? s.hashes)
    ).filter(h => h.trim());
    if (hashes.length === 0) {
      toastStore.show(s.useGlobalHashes
        ? 'Добавьте хеши в Настройках'
        : 'Добавьте хеши профиля или включите глобальные в Настройках'
      );
      return;
    }
    tunnelStore.set('connecting');
    try {
      const workers = s.useGlobalHashes
        ? (s.power || 9)
        : (selected!.power || Math.max(9, hashes.length * 9));
      await WailsConnect({
        profile: selected!.name,
        captchaMode: 'auto',
        workers,
        mtu: s.mtu || 1380,
        hashes,
      });
    } catch {
      tunnelStore.set('idle');
    }
  };

  const [reconnectAt, setReconnectAt] = useState(0); // timestamp когда можно снова подключиться

  const handleTunnel = async () => {
    if (!selected) return;
    if (tunnelState === 'idle') {
      if (Date.now() < reconnectAt) {
        const secs = Math.ceil((reconnectAt - Date.now()) / 1000);
        toastStore.show(`Подождите ${secs} сек.`, 2000);
        return;
      }
      toastStore.show('Убедитесь что другие VPN отключены', 4000);
      await doConnect();
    } else if (tunnelState === 'connected' || tunnelState === 'connecting') {
      tunnelStore.set('disconnecting');
      await WailsDisconnect();
      tunnelStore.set('idle');
      setReconnectAt(Date.now() + 4000);
    }
  };

  const handleAdd = (data: Omit<Server, 'id'>) => {
    const s = serverStore.add(data);
    setServers(serverStore.getAll());
    setSelected(s);
  };

  const handleSave = (server: Server) => {
    serverStore.update(server);
    const all = serverStore.getAll();
    setServers(all);
    if (selected?.id === server.id) setSelected(server);
  };

  const handleDelete = (id: string) => {
    serverStore.remove(id);
    const all = serverStore.getAll();
    setServers(all);
    if (selected?.id === id) setSelected(all[0] ?? null);
  };

  const [iconMenu, setIconMenu] = useState<{ server: Server; x: number; y: number } | null>(null);

  const handleIconClick = (e: React.MouseEvent, server: Server) => {
    e.stopPropagation();
    const rect = (e.currentTarget as HTMLElement).getBoundingClientRect();
    setIconMenu({ server, x: rect.left, y: rect.top });
  };

  const handlePickIcon = (key: string) => {
    if (!iconMenu) return;
    const updated = { ...iconMenu.server, icon: key };
    serverStore.update(updated);
    const all = serverStore.getAll();
    setServers(all);
    if (selected?.id === iconMenu.server.id) setSelected(updated);
    setIconMenu(null);
  };

  const isActive = tunnelState === 'connected';
  const isSpinning = tunnelState === 'connecting' || tunnelState === 'disconnecting';
  const isBusy = tunnelState === 'disconnecting';
  const trafficUsedPct = traffic ? trafficUsedPercent(traffic) : null;

  return (
    <>
      <style>{`
        * { font-family: 'Geist', sans-serif; font-weight: 500; box-sizing: border-box; }
        .main { flex: 1; position: relative; display: flex; align-items: center; justify-content: center; animation: page-in 0.25s ease-out; background: var(--bg); }
        .btn-add { position: absolute; top: 16px; right: 20px; background: none; border: none; cursor: pointer; color: var(--text); }
        .power-btn { position: relative; width: 160px; height: 160px; background: none; border: none; cursor: pointer; display: flex; align-items: center; justify-content: center; padding: 0; transition: opacity 0.2s; }
        .power-btn:disabled { opacity: 0.5; cursor: not-allowed; }
        .orb { position: absolute; width: 130px; height: 130px; }
        .orb img { width: 100%; height: 100%; display: block; }
        .orb--spinning { animation: shape-spin 2s linear infinite; }
        .orb--active { animation: shape-pulse 1.2s ease-in-out infinite; }
        @keyframes shape-spin { from { transform: rotate(0deg); } to { transform: rotate(360deg); } }
        @keyframes shape-pulse { 0%,100% { transform: scale(1); } 50% { transform: scale(1.1); } }
        @keyframes link-flash { 0% { opacity:1; } 30% { opacity:0.2; } 60% { opacity:1; } 80% { opacity:0.4; } 100% { opacity:1; } }
        .orb--flash { animation: link-flash 0.8s ease-out; }
        .power-icon { position: relative; z-index: 1; display: flex; align-items: center; justify-content: center; }
        .status-bar { position: absolute; bottom: 24px; left: 50%; transform: translateX(-50%); display: flex; flex-direction: column; align-items: stretch; width: min(560px, calc(100vw - 24px)); }
        .server-list { border: 1px solid var(--border); border-radius: 12px; overflow: hidden; margin-bottom: 8px; background: var(--surface); animation: slide-down 0.28s ease-out; }
        .server-item { display: flex; align-items: center; gap: 10px; width: 100%; padding: 12px 20px; background: var(--bg-2); font-size: 15px; color: var(--text); font-family: 'Geist', sans-serif; font-weight: 500; border-bottom: 1px solid var(--border-2); }
        .server-item:last-child { border-bottom: none; }
        .server-item:hover { background: var(--bg-3); }
        .server-item--active { background: var(--bg-3); }
        .server-icon-btn { background: none; border: none; cursor: pointer; padding: 0; display: flex; align-items: center; color: var(--text); }
        .server-edit-btn { background: none; border: none; cursor: pointer; padding: 0; display: flex; align-items: center; color: var(--text-3); opacity: 0; transition: opacity 0.15s; }
        .server-item:hover .server-edit-btn { opacity: 1; }
        .status-server { display: flex; flex-direction: column; align-items: stretch; gap: 3px; background: var(--surface); border: 1px solid var(--border); border-radius: 10px; padding: 6px 10px; font-size: 13px; color: var(--text); cursor: pointer; width: 100%; font-family: 'Geist', sans-serif; font-weight: 500; text-align: left; line-height: 1.2; }
        .status-server--empty { color: var(--text-4); }
        .status-row-main { display: flex; align-items: center; gap: 6px; min-width: 0; width: 100%; }
        .status-name { flex: 1; text-align: left; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 13px; }
        .status-submeta { display: flex; align-items: center; gap: 6px; flex-wrap: nowrap; padding-left: 22px; font-size: 11px; color: var(--text-3); width: 100%; min-width: 0; }
        .status-traffic { position: relative; display: inline-flex; align-items: center; flex-shrink: 0; background: var(--bg-2); border: 1px solid var(--border-2); border-radius: 6px; padding: 1px 6px; color: var(--text); font-size: 11px; white-space: nowrap; overflow: hidden; line-height: 1.3; }
        .status-traffic-fill { position: absolute; left: 0; top: 0; bottom: 0; border-radius: 5px; transition: width 0.45s ease, background 0.3s ease; opacity: 0.55; }
        .status-traffic-text { position: relative; z-index: 1; }
        .status-expire { flex: 1 1 auto; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 11px; }
        .status-support { background: none; border: none; cursor: pointer; color: var(--accent); display: flex; padding: 0; margin-left: auto; flex-shrink: 0; }
        .status-ping { display: flex; align-items: center; gap: 4px; font-size: 12px; flex-shrink: 0; }
        .ping-dot { width: 6px; height: 6px; border-radius: 50%; }
        .tunnel-label { position: absolute; top: 50%; left: 50%; transform: translate(-50%, calc(-50% + 80px)); font-size: 13px; color: var(--text-3); pointer-events: none; }
        .traffic-panel { position: absolute; top: 50%; left: 50%; transform: translate(-50%, calc(-50% + 118px)); display: flex; gap: 18px; font-size: 12px; color: var(--text-3); pointer-events: none; white-space: nowrap; }
        .traffic-panel span { display: flex; flex-direction: column; align-items: center; gap: 2px; min-width: 88px; }
        .traffic-panel strong { font-size: 13px; color: var(--text); font-weight: 600; }
        .icon-picker { position: fixed; z-index: 200; background: var(--surface); border: 1px solid var(--border); border-radius: 12px; padding: 10px; box-shadow: var(--shadow); display: grid; grid-template-columns: repeat(6, 36px); gap: 4px; animation: modal-in 0.15s ease-out; }
        .icon-picker-btn { width: 36px; height: 36px; display: flex; align-items: center; justify-content: center; background: none; border: 1px solid transparent; border-radius: 8px; cursor: pointer; color: var(--text); font-size: 18px; }
        .icon-picker-btn:hover { background: var(--bg-3); border-color: var(--border); }
        .icon-picker-btn--active { background: var(--bg-3); border-color: var(--accent); }

      `}</style>
      <main className="main">
        <button className="btn-add" onClick={() => setAddServerOpen(true)}>
          <IconPlus stroke={2} size={22} />
        </button>

        <button
          className="power-btn"
          onClick={handleTunnel}
          disabled={!selected || isBusy}
          title={selected ? TUNNEL_LABEL[tunnelState] : 'Добавьте сервер'}
        >
          <div className={`orb${isSpinning ? ' orb--spinning' : isActive ? ' orb--active' : ''}${linkFlash ? ' orb--flash' : ''}`}>
            <img src={shapeDark} alt="" draggable={false} />
          </div>
          <div className="power-icon">
            <img src={powerIcon} alt="" draggable={false} style={{ width: 28, height: 35 }} />
          </div>
        </button>

        <span className="tunnel-label">{selected ? TUNNEL_LABEL[tunnelState] : 'Нет серверов'}</span>

        {isActive && traffic && (
          <div className="traffic-panel">
            <span>Загружено<strong>{formatBytes(traffic.download)}</strong></span>
            <span>Отправлено<strong>{formatBytes(traffic.upload)}</strong></span>
            <span>Остаток<strong>{trafficRemainLabel(traffic)}</strong></span>
          </div>
        )}

        <div className="status-bar">
          {listOpen && servers.length > 0 && (
            <div className="server-list">
              {servers.map(s => (
                <div
                  key={s.id}
                  className={`server-item${s.id === selected?.id ? ' server-item--active' : ''}`}
                  style={{ cursor: 'pointer' }}
                  onClick={() => { setSelected({ ...s }); setListOpen(false); }}
                >
                  <button className="server-icon-btn" onClick={(e) => { e.stopPropagation(); handleIconClick(e, s); }}>
                    <ServerIcon iconKey={s.icon} size={20} />
                  </button>
                  <span className="status-name">
                    {s.name}
                  </span>
                  {s.ping != null && (
                    <span className="status-ping">
                      <span className="ping-dot" style={{ background: pingColor(s.ping) }} />
                      {s.ping}
                    </span>
                  )}
                  <button className="server-edit-btn" onClick={(e) => { e.stopPropagation(); setEditServer(s); }}>
                    <IconPencil size={15} stroke={2} />
                  </button>
                </div>
              ))}
            </div>
          )}

          <button className={`status-server${!selected ? ' status-server--empty' : ''}`} onClick={() => setListOpen(o => !o)}>
            <div className="status-row-main">
              <ServerIcon iconKey={selected?.icon} size={16} />
              <span className="status-name">{selected ? selected.name : 'Нет серверов'}</span>
              {selected?.ping != null && (
                <span className="status-ping">
                  <span className="ping-dot" style={{ background: pingColor(selected.ping) }} />
                  {selected.ping}
                </span>
              )}
              <IconChevronUp
                size={14}
                style={{ transform: listOpen ? 'rotate(0deg)' : 'rotate(180deg)', transition: 'transform 0.2s', flexShrink: 0 }}
              />
            </div>
            {traffic && selected?.subUrl && (
              <div className="status-submeta" onClick={(e) => e.stopPropagation()}>
                <span
                  className="status-traffic"
                  title={trafficUsedPct != null ? `Использовано ${trafficUsedPct.toFixed(1).replace('.', ',')}%` : 'Использовано / лимит'}
                >
                  {trafficUsedPct != null && (
                    <span
                      className="status-traffic-fill"
                      style={{
                        width: `${trafficUsedPct}%`,
                        minWidth: trafficUsedPct > 0 ? 3 : 0,
                        background: trafficFillColor(trafficUsedPct),
                      }}
                    />
                  )}
                  <span className="status-traffic-text">{trafficCompactLabel(traffic)}</span>
                </span>
                <span className="status-expire" title={expireLabel(traffic)}>{expireLabel(traffic)}</span>
                {traffic.supportUrl && (
                  <span
                    className="status-support"
                    role="button"
                    tabIndex={0}
                    title={traffic.supportUrl}
                    onClick={(e) => { e.stopPropagation(); window.open(traffic.supportUrl, '_blank', 'noopener,noreferrer'); }}
                    onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); e.stopPropagation(); window.open(traffic.supportUrl, '_blank', 'noopener,noreferrer'); } }}
                  >
                    <IconBrandTelegram size={15} stroke={1.7} />
                  </span>
                )}
              </div>
            )}
          </button>
        </div>

        {addServerOpen && <AddServer onClose={() => setAddServerOpen(false)} onAdd={handleAdd} />}
        {editServer && (
          <EditServer
            server={editServer}
            onClose={() => setEditServer(null)}
            onSave={handleSave}
            onDelete={handleDelete}
          />
        )}

        {iconMenu && (
          <>
            <div style={{ position: 'fixed', inset: 0, zIndex: 199 }} onClick={() => setIconMenu(null)} />
            <div
              className="icon-picker"
              style={{
                left: Math.min(iconMenu.x, window.innerWidth - 256),
                top: iconMenu.y - 4 - (Math.ceil(SERVER_ICONS.length / 6) * 40 + 20),
              }}
            >
              {SERVER_ICONS.map(ic => (
                <button
                  key={ic.key}
                  className={`icon-picker-btn${(iconMenu.server.icon ?? 'clover') === ic.key ? ' icon-picker-btn--active' : ''}`}
                  onClick={() => handlePickIcon(ic.key)}
                  title={ic.key}
                >
                  {ic.render(18)}
                </button>
              ))}
            </div>
          </>
        )}
      </main>

    </>
  );
}
