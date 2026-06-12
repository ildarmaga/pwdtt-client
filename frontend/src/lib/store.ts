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

export const settingsStore = {
  get: (): AppSettings => {
    const saved = parse<Partial<AppSettings>>(SETTINGS_KEY, {});
    const merged = { ...DEFAULT_SETTINGS, ...saved };
    // ensure hashes is always exactly 4 strings
    const h = Array.isArray(merged.hashes) ? merged.hashes : [];
    merged.hashes = [h[0] ?? '', h[1] ?? '', h[2] ?? '', h[3] ?? ''];
    return merged;
  },
  save: (settings: AppSettings) => localStorage.setItem(SETTINGS_KEY, JSON.stringify(settings)),
};
