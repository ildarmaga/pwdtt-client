import type { Server, AppSettings } from './types';
import { DEFAULT_SETTINGS } from './types';

const SERVERS_KEY = 'wdtt_servers';
const SETTINGS_KEY = 'wdtt_settings';

function newId(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID();
  }
  return `id-${Date.now()}-${Math.random().toString(36).slice(2, 11)}`;
}

function parse<T>(key: string, fallback: T): T {
  try {
    const raw = localStorage.getItem(key);
    return raw ? (JSON.parse(raw) as T) : fallback;
  } catch {
    return fallback;
  }
}

export const serverStore = {
  getAll: (): Server[] => parse<Server[]>(SERVERS_KEY, []),
  save: (servers: Server[]) => localStorage.setItem(SERVERS_KEY, JSON.stringify(servers)),
  add: (server: Omit<Server, 'id'>): Server => {
    const s: Server = { ...server, id: newId() };
    const all = serverStore.getAll();
    serverStore.save([...all, s]);
    return s;
  },
  update: (server: Server) => {
    serverStore.save(serverStore.getAll().map(s => s.id === server.id ? server : s));
  },
  remove: (id: string) => {
    serverStore.save(serverStore.getAll().filter(s => s.id !== id));
  },
};

type SettingsListener = (s: AppSettings) => void;
const settingsListeners = new Set<SettingsListener>();

export const settingsStore = {
  get: (): AppSettings => {
    const saved = parse<Partial<AppSettings>>(SETTINGS_KEY, {});
    const merged = { ...DEFAULT_SETTINGS, ...saved };
    // ensure hashes is always exactly 4 strings
    const h = Array.isArray(merged.hashes) ? merged.hashes : [];
    merged.hashes = [h[0] ?? '', h[1] ?? '', h[2] ?? '', h[3] ?? ''];
    if (typeof merged.metricsRefreshSec !== 'number' || merged.metricsRefreshSec < 0) {
      merged.metricsRefreshSec = DEFAULT_SETTINGS.metricsRefreshSec;
    }
    if (merged.tunnelProtocol !== 'vk' && merged.tunnelProtocol !== 'wb') {
      merged.tunnelProtocol = DEFAULT_SETTINGS.tunnelProtocol;
    }
    if (merged.obfsMode !== 'audio' && merged.obfsMode !== 'video') {
      merged.obfsMode = DEFAULT_SETTINGS.obfsMode;
    }
    if (merged.tunnelMode !== 'wg' && merged.tunnelMode !== 'raw') {
      merged.tunnelMode = DEFAULT_SETTINGS.tunnelMode;
    }
    if (typeof merged.wbFps !== 'number' || merged.wbFps < 1 || merged.wbFps > 120) {
      merged.wbFps = DEFAULT_SETTINGS.wbFps;
    }
    if (typeof merged.wbBatch !== 'number' || merged.wbBatch < 1 || merged.wbBatch > 256) {
      merged.wbBatch = DEFAULT_SETTINGS.wbBatch;
    }
    // rev=4: RelayBridge defaults dual-track OFF (kulikov0). Forced dual=true
    // sharded MsgData onto screenshare; SFU drops → SOCKS ClientHello lost.
    if (saved.wbVp8Rev !== DEFAULT_SETTINGS.wbVp8Rev) {
      merged.wbDualTrack = DEFAULT_SETTINGS.wbDualTrack;
      merged.wbFps = DEFAULT_SETTINGS.wbFps;
      merged.wbBatch = DEFAULT_SETTINGS.wbBatch;
      merged.wbVp8Rev = DEFAULT_SETTINGS.wbVp8Rev;
      try { localStorage.setItem(SETTINGS_KEY, JSON.stringify(merged)); } catch { /* ignore */ }
    }
    if (typeof merged.wbDualTrack !== 'boolean') {
      merged.wbDualTrack = DEFAULT_SETTINGS.wbDualTrack;
    }
    if (merged.wbProxyAuth !== 'auto' && merged.wbProxyAuth !== 'manual') {
      merged.wbProxyAuth = DEFAULT_SETTINGS.wbProxyAuth;
    }
    if (typeof merged.wbSocksOnly !== 'boolean') {
      merged.wbSocksOnly = DEFAULT_SETTINGS.wbSocksOnly;
    }
    // Built-in TUN removed — always SOCKS-only.
    merged.wbSocksOnly = true;
    if (typeof merged.wbSocksPort !== 'number' || merged.wbSocksPort < 1024 || merged.wbSocksPort > 65535) {
      merged.wbSocksPort = DEFAULT_SETTINGS.wbSocksPort;
    }
    // Migrate old default 10808 (often clashes with v2rayN inbound) → 10809.
    if (merged.wbSocksPort === 10808) {
      merged.wbSocksPort = DEFAULT_SETTINGS.wbSocksPort;
      try { localStorage.setItem(SETTINGS_KEY, JSON.stringify(merged)); } catch { /* ignore */ }
    }
    return merged;
  },
  save: (settings: AppSettings) => {
    localStorage.setItem(SETTINGS_KEY, JSON.stringify(settings));
    settingsListeners.forEach(fn => fn(settings));
  },
  patch: (partial: Partial<AppSettings>) => {
    settingsStore.save({ ...settingsStore.get(), ...partial });
  },
  subscribe: (fn: SettingsListener) => {
    settingsListeners.add(fn);
    fn(settingsStore.get());
    return () => { settingsListeners.delete(fn); };
  },
};
