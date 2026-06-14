import { useState, useEffect, useRef, useMemo } from 'react';
import type React from 'react';
import {
  IconCloverFilled, IconPlus, IconChevronUp, IconPencil,
  IconFlameFilled, IconShieldFilled, IconLayoutGridFilled, IconCloudFilled, IconBrandSpeedtest,
  IconStarFilled, IconHeartFilled, IconBoltFilled, IconRocket,
  IconCrownFilled, IconDiamondFilled, IconLeafFilled, IconSnowflake,
  IconServer, IconGlobe, IconLockFilled, IconWifi, IconBrandTelegram, IconPower, IconLock,
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
import { activeServerStore } from '../lib/stores/activeServerStore';
import { selectedServerStore } from '../lib/stores/selectedServerStore';
import { tunnelStatsStore, formatRate, formatMs, type TunnelStats } from '../lib/stores/tunnelStatsStore';
import { trafficStatsStore } from '../lib/stores/trafficStatsStore';
import { toastStore } from '../lib/stores/toastStore';
import ConnectionErrorBanner from '../components/ConnectionErrorBanner';
import { wdttLinkStore, fetchTrafficStats, formatBytes, trafficCompactLabel, trafficUsedPercent, trafficFillColor, expireLabel, metricsRefreshMs, serverVpnTitle, type TrafficStats } from '../lib/utils/wdttLink';
import { SaveProfile } from '../../wailsjs/go/backend/App';
import type { Server, TunnelState } from '../lib/types';
import { Connect as WailsConnect, Disconnect as WailsDisconnect } from '../../wailsjs/go/backend/App';

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
    if (all.length === 0) return null;
    // Восстанавливаем ранее выбранный сервер, чтобы он не слетал на первый
    // при возврате на страницу (особенно когда туннель подключён).
    const savedId = selectedServerStore.getId();
    return all.find(s => s.id === savedId) ?? all[0];
  });
  const [listOpen, setListOpen] = useState(false);

  // Персистим выбранный сервер: переживает смену роута Connect ↔ Logs.
  useEffect(() => {
    selectedServerStore.setId(selected?.id ?? null);
  }, [selected?.id]);

  // tunnelState из глобального store — переживает смену роута
  const [tunnelState, setTunnelState] = useState<TunnelState>(() => tunnelStore.get());
  useEffect(() => tunnelStore.subscribe(setTunnelState), []);

  const [activeServerId, setActiveServerId] = useState<string | null>(() => activeServerStore.getId());
  useEffect(() => activeServerStore.subscribe(setActiveServerId), []);

  /** Пока VPN активен — панель и метрики всегда от активного профиля, не от последнего добавленного. */
  const displayServer = useMemo(() => {
    if (tunnelState !== 'idle' && activeServerId) {
      return servers.find(s => s.id === activeServerId) ?? selected;
    }
    return selected;
  }, [tunnelState, activeServerId, servers, selected]);

  const listHighlightId = tunnelState !== 'idle' && activeServerId ? activeServerId : selected?.id;

  // Смена сервера (только в idle): сброс метрик сессии и кулдауна.
  useEffect(() => {
    if (tunnelState !== 'idle') return;
    tunnelStatsStore.reset();
    setReconnectAt(0);
    if (selected?.subUrl) {
      setTraffic(trafficStatsStore.get(selected.subUrl));
    } else {
      setTraffic(null);
    }
  }, [selected?.id, selected?.subUrl, tunnelState]);

  const [addServerOpen, setAddServerOpen] = useState(false);
  const [editServer, setEditServer] = useState<Server | null>(null);
  const [linkFlash, setLinkFlash] = useState(false);
  const [traffic, setTraffic] = useState<TrafficStats | null>(() => trafficStatsStore.get(selected?.subUrl));
  const [sessionStats, setSessionStats] = useState<TunnelStats | null>(() => tunnelStatsStore.get());
  const [metricsRefreshSec, setMetricsRefreshSec] = useState(() => settingsStore.get().metricsRefreshSec);
  useEffect(() => settingsStore.subscribe(s => setMetricsRefreshSec(s.metricsRefreshSec)), []);
  useEffect(() => tunnelStatsStore.subscribe(setSessionStats), []);

  useEffect(() => {
    return wdttLinkStore.subscribe((link) => {
      if (!link) return;
      const consumed = wdttLinkStore.consume();
      if (!consumed) return;
      const host = `${consumed.ip}:${consumed.dtlsPort}`;
      const name = consumed.name;
      const vpnName = consumed.vpnName;

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
        let s: Server;
        if (existing) {
          s = {
            ...existing,
            name,
            vpnName: vpnName ?? existing.vpnName,
            password: consumed.password,
            subUrl: consumed.subUrl ?? existing.subUrl,
            hashes: consumed.hashes.length > 0 ? padded : existing.hashes,
            linkManaged: true,
          };
          serverStore.update(s);
        } else {
          s = serverStore.add({
            name,
            vpnName,
            host,
            password: consumed.password,
            subUrl: consumed.subUrl,
            hashes: consumed.hashes.length > 0 ? padded : undefined,
            linkManaged: true,
          });
        }
        if (consumed.stats && consumed.subUrl) {
          trafficStatsStore.set(consumed.subUrl, consumed.stats);
        }
        setServers(serverStore.getAll());
        const vpnIdle = tunnelStore.get() === 'idle';
        if (vpnIdle) {
          if (consumed.stats) setTraffic(consumed.stats);
          setSelected({ ...s });
          setLinkFlash(true);
          setTimeout(() => setLinkFlash(false), 800);
        }
        toastStore.show(
          existing
            ? `Профиль обновлён: ${name}${vpnIdle ? '' : ' (подключение не изменено)'}`
            : `Профиль добавлен: ${name}${vpnIdle ? '' : ' (подключение не изменено)'}`,
          3000,
        );
      };
      applyLink();
    });
  }, []);

  useEffect(() => {
    if (!displayServer?.subUrl) {
      if (tunnelState === 'idle') setTraffic(null);
      return;
    }
    const subUrl = displayServer.subUrl;
    const cached = trafficStatsStore.get(subUrl);
    if (cached) setTraffic(cached);
    let cancelled = false;
    let timer = 0;
    const fallbackMs = (metricsRefreshSec > 0 ? metricsRefreshSec : 5) * 1000;
    const tick = async () => {
      let nextMs = fallbackMs;
      try {
        const stats = await fetchTrafficStats(subUrl);
        if (cancelled) return;
        if (stats) {
          setTraffic(stats);
          trafficStatsStore.set(subUrl, stats);
          nextMs = metricsRefreshMs(stats, metricsRefreshSec);
        }
      } finally {
        if (!cancelled) timer = window.setTimeout(tick, nextMs);
      }
    };
    tick();
    return () => {
      cancelled = true;
      window.clearTimeout(timer);
    };
  }, [displayServer?.subUrl, displayServer?.id, metricsRefreshSec, tunnelState]);

  useEffect(() => {
    if (tunnelState !== 'connected' || !displayServer?.subUrl) return;
    let cancelled = false;
    fetchTrafficStats(displayServer.subUrl).then((stats) => {
      if (!cancelled && stats) {
        setTraffic(stats);
        trafficStatsStore.set(displayServer.subUrl!, stats);
      }
    });
    return () => { cancelled = true; };
  }, [tunnelState, displayServer?.subUrl, displayServer?.id]);

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
    activeServerStore.setId(selected!.id);
    try {
      const workers = s.useGlobalHashes
        ? (s.power || 9)
        : (selected!.power || Math.max(9, hashes.length * 9));
      await WailsConnect({
        profile: selected!.name,
        captchaMode: 'auto',
        workers,
        mtu: s.mtu || 1280,
        hashes,
      });
    } catch (e) {
      tunnelStore.set('idle');
      activeServerStore.setId(null);
      const msg = e instanceof Error ? e.message : String(e ?? '');
      toastStore.show(msg || 'Не удалось подключиться', 4000);
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
      setReconnectAt(Date.now() + 2000);
    }
  };

  const handleAdd = (data: Omit<Server, 'id'>) => {
    const s = serverStore.add(data);
    setServers(serverStore.getAll());
    if (tunnelStore.get() === 'idle') {
      setSelected(s);
    } else {
      toastStore.show(`Добавлено: ${s.name}. Переключение — после отключения`, 3000);
    }
  };

  const handleSave = (server: Server) => {
    serverStore.update(server);
    const all = serverStore.getAll();
    setServers(all);
    if (selected?.id === server.id) setSelected(server);
  };

  const handleDelete = (id: string) => {
    if (tunnelStore.get() !== 'idle' && activeServerStore.getId() === id) {
      toastStore.show('Сначала отключитесь от этого сервера', 3000);
      return;
    }
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
  // Пока туннель не отключён — сервер зафиксирован, выбор недоступен.
  const selectionLocked = tunnelState !== 'idle';
  useEffect(() => {
    if (selectionLocked) setListOpen(false);
  }, [selectionLocked]);
  const trafficUsedPct = traffic ? trafficUsedPercent(traffic) : null;
  const statsLive = isActive && sessionStats;
  const rxDisplay = statsLive ? formatBytes(sessionStats!.rxBytes) : '—';
  const txDisplay = statsLive ? formatBytes(sessionStats!.txBytes) : '—';
  const rxRateDisplay = statsLive ? formatRate(sessionStats!.rxRate) : '—';
  const txRateDisplay = statsLive ? formatRate(sessionStats!.txRate) : '—';
  const turnDisplay = statsLive ? formatMs(sessionStats!.turnRttMs) : '—';
  const dtlsDisplay = statsLive ? formatMs(sessionStats!.dtlsHsMs) : '—';
  const netDisplay = statsLive ? formatMs(sessionStats!.internetRttMs) : '—';

  // Блок кнопки+метрик держится на фиксированном расстоянии над панелью сервера.
  // При длинном объявлении панель растёт вверх — поднимаем блок ровно на её высоту.
  const statusInfoRef = useRef<HTMLDivElement>(null);
  const STATUS_BOTTOM = 24; // отступ панели от низа окна
  const STACK_GAP = 18;     // зазор между панелью и блоком метрик
  const [stackBottom, setStackBottom] = useState(128);
  useEffect(() => {
    const el = statusInfoRef.current;
    if (!el) return;
    const update = () => {
      const h = el.getBoundingClientRect().height;
      setStackBottom(Math.round(STATUS_BOTTOM + h + STACK_GAP));
    };
    update();
    const ro = new ResizeObserver(update);
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  return (
    <>
      <style>{`
        * { font-family: 'Geist', sans-serif; font-weight: 500; box-sizing: border-box; }
        .main { flex: 1; position: relative; display: flex; align-items: center; justify-content: center; animation: page-in 0.25s ease-out; background: var(--bg); }
        .connect-center {
          position: absolute;
          left: 50%;
          bottom: 128px;
          transition: bottom 0.22s ease;
          transform: translateX(-50%);
          display: flex;
          flex-direction: column;
          align-items: center;
          gap: 12px;
          width: min(340px, calc(100vw - 32px));
        }
        .connect-stack { display: flex; flex-direction: column; align-items: center; gap: 10px; }
        .btn-add { position: absolute; top: 16px; right: 20px; background: none; border: none; cursor: pointer; color: var(--text); }
        .connect-btn {
          position: relative;
          width: 140px;
          height: 140px;
          border-radius: 50%;
          border: none;
          cursor: pointer;
          display: flex;
          align-items: center;
          justify-content: center;
          padding: 0;
          background: linear-gradient(145deg, var(--bg-3), var(--bg-2));
          box-shadow: 0 8px 32px rgba(0,0,0,0.45), inset 0 1px 0 rgba(255,255,255,0.06);
          transition: box-shadow 0.4s ease, transform 0.15s ease;
          color: var(--text-3);
        }
        .connect-btn:hover:not(:disabled) { transform: scale(1.03); }
        .connect-btn:active:not(:disabled) { transform: scale(0.97); }
        .connect-btn:disabled { opacity: 0.45; cursor: not-allowed; }
        .connect-btn__icon { position: relative; z-index: 2; transition: color 0.35s ease; }
        .connect-btn__halo {
          position: absolute;
          inset: -6px;
          border-radius: 50%;
          opacity: 0;
          pointer-events: none;
          background: radial-gradient(circle, rgba(34,197,94,0.28), transparent 68%);
          transition: opacity 0.4s ease;
        }
        .connect-btn__ring {
          position: absolute;
          inset: -3px;
          border-radius: 50%;
          border: 2px solid transparent;
          opacity: 0;
          pointer-events: none;
        }
        .connect-btn--on {
          color: #22c55e;
          box-shadow: 0 0 44px rgba(34,197,94,0.32), 0 8px 32px rgba(0,0,0,0.45), inset 0 1px 0 rgba(255,255,255,0.08);
        }
        .connect-btn--on .connect-btn__halo {
          opacity: 1;
          animation: connect-halo-pulse 2.5s ease-in-out infinite;
        }
        .connect-btn--busy {
          color: var(--accent);
          box-shadow: 0 0 28px color-mix(in srgb, var(--accent) 35%, transparent), 0 8px 32px rgba(0,0,0,0.45);
        }
        .connect-btn--busy .connect-btn__ring {
          opacity: 1;
          border-top-color: var(--accent);
          border-right-color: color-mix(in srgb, var(--accent) 40%, transparent);
          animation: connect-ring-spin 1.1s linear infinite;
        }
        .connect-btn--busy .connect-btn__halo {
          opacity: 0.55;
          background: radial-gradient(circle, color-mix(in srgb, var(--accent) 30%, transparent), transparent 68%);
          animation: connect-halo-pulse 1.4s ease-in-out infinite;
        }
        .connect-btn--flash {
          animation: connect-flash 0.8s ease-out;
        }
        @keyframes connect-halo-pulse {
          0%, 100% { transform: scale(1); opacity: 0.55; }
          50% { transform: scale(1.08); opacity: 1; }
        }
        @keyframes connect-ring-spin { to { transform: rotate(360deg); } }
        @keyframes connect-flash {
          0%, 100% { opacity: 1; }
          35% { opacity: 0.35; }
          65% { opacity: 1; }
        }
        .status-bar { position: absolute; bottom: 24px; left: 50%; transform: translateX(-50%); display: flex; flex-direction: column; align-items: stretch; width: min(420px, calc(100vw - 24px)); }
        .status-info { display: flex; flex-direction: column; align-items: stretch; }
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
        .status-server--locked { cursor: default; }
        .status-row-main { display: flex; align-items: center; gap: 6px; min-width: 0; width: 100%; }
        .status-name { flex: 1; text-align: left; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 13px; }
        .status-submeta { display: flex; align-items: center; gap: 6px; flex-wrap: nowrap; padding-left: 22px; font-size: 11px; color: var(--text-3); width: 100%; min-width: 0; }
        .status-traffic { position: relative; display: inline-flex; align-items: center; flex-shrink: 0; background: var(--bg-2); border: 1px solid var(--border-2); border-radius: 6px; padding: 1px 6px; color: var(--text); font-size: 11px; white-space: nowrap; overflow: hidden; line-height: 1.3; }
        .status-traffic-fill { position: absolute; left: 0; top: 0; bottom: 0; border-radius: 5px; transition: width 0.45s ease, background 0.3s ease; opacity: 0.55; }
        .status-traffic-text { position: relative; z-index: 1; }
        .status-expire { flex: 1 1 auto; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 11px; }
        .status-announce { margin-top: 6px; padding: 8px 12px; font-size: 11px; color: var(--text-3); background: var(--surface); border: 1px solid var(--border); border-radius: 10px; width: 100%; min-width: 0; line-height: 1.4; text-align: center; overflow: hidden; display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; }
        .status-support { background: none; border: none; cursor: pointer; color: var(--accent); display: flex; padding: 0; margin-left: auto; flex-shrink: 0; }
        .status-ping { display: flex; align-items: center; gap: 4px; font-size: 12px; flex-shrink: 0; }
        .ping-dot { width: 6px; height: 6px; border-radius: 50%; }
        .tunnel-label { font-size: 13px; color: var(--text-3); pointer-events: none; line-height: 1.2; }
        .session-stats {
          width: 100%;
          background: var(--surface);
          border: 1px solid var(--border);
          border-radius: 14px;
          padding: 12px 14px;
          pointer-events: none;
        }
        .session-stats--idle .session-stat-value,
        .session-stats--idle .session-stat-sub,
        .session-stats--idle .session-latency strong { color: var(--text-4); }
        .session-stats--idle .session-stat-sub { color: var(--text-4); }
        .session-stats-grid { display: flex; justify-content: space-between; align-items: flex-start; gap: 16px; margin-bottom: 10px; }
        .session-stat { display: flex; flex-direction: column; gap: 2px; min-width: 0; }
        .session-stat--right { align-items: flex-end; text-align: right; }
        .session-stat-label { font-size: 10px; letter-spacing: 0.04em; text-transform: uppercase; color: var(--text-4); }
        .session-stat-value { font-size: 15px; font-weight: 600; color: var(--text); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
        .session-stat-sub { font-size: 11px; color: var(--accent); }
        .session-latency { display: flex; justify-content: space-between; gap: 8px; padding-top: 10px; border-top: 1px solid var(--border-2); font-size: 11px; color: var(--text-3); }
        .session-latency span { display: flex; flex-direction: column; align-items: center; gap: 2px; flex: 1; min-width: 0; }
        .session-latency strong { font-size: 12px; color: var(--text); font-weight: 600; }
        .icon-picker { position: fixed; z-index: 200; background: var(--surface); border: 1px solid var(--border); border-radius: 12px; padding: 10px; box-shadow: var(--shadow); display: grid; grid-template-columns: repeat(6, 36px); gap: 4px; animation: modal-in 0.15s ease-out; }
        .icon-picker-btn { width: 36px; height: 36px; display: flex; align-items: center; justify-content: center; background: none; border: 1px solid transparent; border-radius: 8px; cursor: pointer; color: var(--text); font-size: 18px; }
        .icon-picker-btn:hover { background: var(--bg-3); border-color: var(--border); }
        .icon-picker-btn--active { background: var(--bg-3); border-color: var(--accent); }
        .connect-error {
          position: absolute;
          top: 52px;
          left: 50%;
          transform: translateX(-50%);
          width: min(420px, calc(100vw - 32px));
          display: flex;
          align-items: flex-start;
          gap: 10px;
          padding: 12px 38px 12px 12px;
          background: color-mix(in srgb, #ef4444 14%, var(--surface));
          border: 1px solid color-mix(in srgb, #ef4444 45%, var(--border));
          border-radius: 12px;
          z-index: 40;
          animation: error-banner-in 0.28s ease-out;
          box-shadow: 0 8px 28px rgba(0,0,0,0.28);
        }
        .connect-error__icon { flex-shrink: 0; color: #f87171; margin-top: 1px; }
        .connect-error__body { min-width: 0; flex: 1; }
        .connect-error__title { font-size: 12px; font-weight: 600; color: #fca5a5; margin-bottom: 4px; letter-spacing: 0.02em; }
        .connect-error__text { font-size: 13px; line-height: 1.45; color: var(--text); word-break: break-word; }
        .connect-error__close {
          position: absolute;
          top: 8px;
          right: 8px;
          background: none;
          border: none;
          color: var(--text-3);
          cursor: pointer;
          padding: 4px;
          border-radius: 6px;
          display: flex;
        }
        .connect-error__close:hover { color: var(--text); background: var(--bg-3); }
        @keyframes error-banner-in {
          from { opacity: 0; transform: translateX(-50%) translateY(-8px); }
          to { opacity: 1; transform: translateX(-50%) translateY(0); }
        }

      `}</style>
      <main className="main">
        <button className="btn-add" onClick={() => setAddServerOpen(true)}>
          <IconPlus stroke={2} size={22} />
        </button>

        <ConnectionErrorBanner />

        <div className="connect-center" style={{ bottom: stackBottom }}>
          <div className="connect-stack">
            <button
              className={`connect-btn${isActive ? ' connect-btn--on' : ''}${isSpinning ? ' connect-btn--busy' : ''}${linkFlash ? ' connect-btn--flash' : ''}`}
              onClick={handleTunnel}
              disabled={!selected || isBusy}
              title={selected ? TUNNEL_LABEL[tunnelState] : 'Добавьте сервер'}
              aria-label={selected ? TUNNEL_LABEL[tunnelState] : 'Добавьте сервер'}
            >
              <span className="connect-btn__halo" aria-hidden />
              <span className="connect-btn__ring" aria-hidden />
              <IconPower className="connect-btn__icon" size={36} stroke={2} />
            </button>

            <span className="tunnel-label">{selected ? TUNNEL_LABEL[tunnelState] : 'Нет серверов'}</span>
          </div>

          <div className={`session-stats${statsLive ? '' : ' session-stats--idle'}`}>
          <div className="session-stats-grid">
            <div className="session-stat">
              <span className="session-stat-label">↓ Загружено</span>
              <span className="session-stat-value">{rxDisplay}</span>
              <span className="session-stat-sub">{rxRateDisplay}</span>
            </div>
            <div className="session-stat session-stat--right">
              <span className="session-stat-label">↑ Отправлено</span>
              <span className="session-stat-value">{txDisplay}</span>
              <span className="session-stat-sub">{txRateDisplay}</span>
            </div>
          </div>
          <div className="session-latency">
            <span title="TURN Allocate RTT">
              TURN
              <strong>{turnDisplay}</strong>
            </span>
            <span title="DTLS Handshake">
              DTLS
              <strong>{dtlsDisplay}</strong>
            </span>
            <span title="TCP до 1.1.1.1">
              Интернет
              <strong>{netDisplay}</strong>
            </span>
          </div>
        </div>
        </div>

        <div className="status-bar">
          {listOpen && servers.length > 0 && (
            <div className="server-list">
              {servers.map(s => (
                <div
                  key={s.id}
                  className={`server-item${s.id === listHighlightId ? ' server-item--active' : ''}`}
                  style={{ cursor: 'pointer' }}
                  onClick={() => {
                    if (selectionLocked) return;
                    setSelected({ ...s });
                    setListOpen(false);
                  }}
                >
                  <button className="server-icon-btn" onClick={(e) => { e.stopPropagation(); handleIconClick(e, s); }}>
                    <ServerIcon iconKey={s.icon} size={20} />
                  </button>
                  <span className="status-name">
                    {serverVpnTitle(s, null)}
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

          <div ref={statusInfoRef} className="status-info">
          <button
            className={`status-server${!displayServer ? ' status-server--empty' : ''}${selectionLocked ? ' status-server--locked' : ''}`}
            onClick={() => { if (!selectionLocked) setListOpen(o => !o); }}
            title={selectionLocked ? 'Сервер зафиксирован — отключитесь, чтобы сменить' : undefined}
          >
            <div className="status-row-main">
              <ServerIcon iconKey={displayServer?.icon} size={16} />
              <span className="status-name">{serverVpnTitle(displayServer, traffic)}</span>
              {displayServer?.ping != null && (
                <span className="status-ping">
                  <span className="ping-dot" style={{ background: pingColor(displayServer.ping) }} />
                  {displayServer.ping}
                </span>
              )}
              {selectionLocked ? (
                <IconLock size={13} style={{ flexShrink: 0, opacity: 0.6 }} />
              ) : (
                <IconChevronUp
                  size={14}
                  style={{ transform: listOpen ? 'rotate(0deg)' : 'rotate(180deg)', transition: 'transform 0.2s', flexShrink: 0 }}
                />
              )}
            </div>
            {traffic && displayServer?.subUrl && (
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
          {traffic?.announce && displayServer?.subUrl && (
            <div className="status-announce" title={traffic.announce}>
              {traffic.announce}
            </div>
          )}
          </div>
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
