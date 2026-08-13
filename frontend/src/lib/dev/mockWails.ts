/** Browser-only Vite preview: stub Wails runtime when window.go is missing. */

import { parseWdttFromSubBody } from '../utils/wdttLink';
import { DEFAULT_SETTINGS } from '../types';
import { settingsStore } from '../store';
import {
  devFetchSubscriptionStats,
  devFetchSubscriptionURL,
  markDevSubConnected,
} from './mockSubscription';

declare global {
  interface Window {
    runtime?: Record<string, unknown>;
    go?: { backend: { App: Record<string, (...args: unknown[]) => unknown> } };
    __pwdttDevConnected?: boolean;
  }
}

export const isBrowserDev = typeof window !== 'undefined' && !window.go?.backend?.App;

function noop() {}

function asyncVoid() {
  return Promise.resolve();
}

let emitDevEvent: (name: string, ...args: unknown[]) => void = noop;
let devStatsTimer = 0;
let devStatsRx = 0;
let devStatsTx = 0;

function startDevTunnelStats() {
  window.clearInterval(devStatsTimer);
  devStatsRx = 0;
  devStatsTx = 0;
  devStatsTimer = window.setInterval(() => {
    devStatsRx += 180_000 + Math.random() * 120_000;
    devStatsTx += 45_000 + Math.random() * 35_000;
    emitDevEvent('tunnel_stats', devStatsRx, devStatsTx, 9, 36, Date.now(), 142 + Math.random() * 40, 68 + Math.random() * 30, 28 + Math.random() * 20);
  }, 2000);
}

function stopDevTunnelStats() {
  window.clearInterval(devStatsTimer);
  emitDevEvent('tunnel_stats', 0, 0, 0, 0, 0, 0, 0, 0);
}

function installRuntimeMock() {
  if (window.runtime?.EventsOnMultiple) return;

  const listeners = new Map<string, Set<(...args: unknown[]) => void>>();

  emitDevEvent = (eventName: string, ...args: unknown[]) => {
    listeners.get(eventName)?.forEach(fn => fn(...args));
  };

  window.runtime = {
    EventsOnMultiple(eventName: string, callback: (...args: unknown[]) => void) {
      if (!listeners.has(eventName)) listeners.set(eventName, new Set());
      listeners.get(eventName)!.add(callback);
      return () => listeners.get(eventName)?.delete(callback);
    },
    EventsOff: noop,
    EventsOffAll: noop,
    EventsEmit: emitDevEvent,
    LogPrint: noop,
    LogTrace: noop,
    LogDebug: noop,
    LogInfo: noop,
    LogWarning: noop,
    LogError: noop,
    LogFatal: noop,
    BrowserOpenURL(url: string) {
      window.open(url, '_blank', 'noopener,noreferrer');
    },
    ClipboardGetText: async () => {
      try {
        return await navigator.clipboard.readText();
      } catch {
        return '';
      }
    },
  };
}

function seedDevSettings() {
  try {
    const key = 'wdtt_settings';
    const raw = localStorage.getItem(key);
    const saved = raw ? JSON.parse(raw) : {};
    if (!saved.hashes?.[0]?.trim()) {
      localStorage.setItem(key, JSON.stringify({
        ...DEFAULT_SETTINGS,
        ...saved,
        useGlobalHashes: false,
        hashes: ['', '', '', ''],
      }));
    }
  } catch { /* ignore */ }
}

const VK_USE_COOKIES_KEY = 'pwdtt_vk_use_cookies';
const VK_COOKIES_RAW_KEY = 'pwdtt_vk_cookies_raw';

function readVkUseCookies(): boolean {
  try { return localStorage.getItem(VK_USE_COOKIES_KEY) === '1'; } catch { return false; }
}

function readVkCookiesRaw(): string {
  try { return localStorage.getItem(VK_COOKIES_RAW_KEY) || ''; } catch { return ''; }
}

function vkCookiesHasRemix(raw: string): boolean {
  return /remixsid/i.test(raw);
}

function installGoMock() {
  if (window.go?.backend?.App) return;

  window.__pwdttDevConnected = false;

  window.go = {
    backend: {
      App: {
        CheckVPN: async () => '',
        Connect: async () => {
          const proto = settingsStore.get().tunnelProtocol;
          emitDevEvent('log', 'INFO', proto === 'vk' ? 'Подключение VK…' : 'Подключение WB Stream…');
          emitDevEvent('state_changed', 'connecting');
          await new Promise(r => setTimeout(r, 900));
          window.__pwdttDevConnected = true;
          emitDevEvent('state_changed', 'running');
          if (proto === 'vk') {
            emitDevEvent('log', 'GO', '[dev] turn: allocate OK');
            emitDevEvent('log', 'GO', '[dev] dtls: handshake OK');
            emitDevEvent('log', 'STATUS', 'TUNNEL_CONNECTED');
            emitDevEvent('log', 'INFO', '✓ VK туннель активен');
          } else {
            emitDevEvent('log', 'GO', '[dev] wbt: dial OK');
            emitDevEvent('log', 'GO', '[dev] webrtc: offer/answer OK');
            emitDevEvent('log', 'STATUS', 'WB_TUNNEL_CONNECTED');
            emitDevEvent('log', 'INFO', '✓ WB Stream активен');
          }
          startDevTunnelStats();
        },
        ConnectWB: async (_room: unknown, _routing: unknown, _fps: unknown, _batch: unknown, _dual: unknown, _socksOnly: unknown, socksPort: unknown, socksUser: unknown, socksPass: unknown, _deviceId?: unknown, _password?: unknown) => {
          emitDevEvent('log', 'INFO', 'Подключение WB Stream (SOCKS для v2rayN)…');
          emitDevEvent('state_changed', 'connecting');
          await new Promise(r => setTimeout(r, 700));
          window.__pwdttDevConnected = true;
          const port = Number(socksPort) || 10809;
          const user = String(socksUser || 'devuser');
          const pass = String(socksPass || 'devpass');
          emitDevEvent('wb_socks_ready', '127.0.0.1', port, user, pass);
          emitDevEvent('log', 'INFO', `✓ SOCKS5 127.0.0.1:${port}`);
          emitDevEvent('state_changed', 'running');
          startDevTunnelStats();
        },
        Disconnect: async () => {
          const proto = settingsStore.get().tunnelProtocol;
          stopDevTunnelStats();
          emitDevEvent('state_changed', 'disconnecting');
          emitDevEvent('log', 'INFO', proto === 'vk' ? 'Отключение VK…' : 'Отключение WB…');
          await new Promise(r => setTimeout(r, 400));
          window.__pwdttDevConnected = false;
          emitDevEvent('wb_socks_ready', '', 0, '', '');
          emitDevEvent('state_changed', 'stopped');
          emitDevEvent('log', 'STATUS', 'DISCONNECTED');
          emitDevEvent('log', 'INFO', '— Отключено');
        },
        DisconnectWB: async () => {
          stopDevTunnelStats();
          emitDevEvent('state_changed', 'disconnecting');
          emitDevEvent('log', 'INFO', 'Отключение WB…');
          await new Promise(r => setTimeout(r, 400));
          window.__pwdttDevConnected = false;
          emitDevEvent('wb_socks_ready', '', 0, '', '');
          emitDevEvent('state_changed', 'stopped');
          emitDevEvent('log', 'INFO', '— Отключено');
        },
        GetWBSocksEndpoint: async () => ({ ok: false }),
        Reconnect: async () => {
          emitDevEvent('state_changed', 'connecting');
          await new Promise(r => setTimeout(r, 600));
          window.__pwdttDevConnected = true;
          emitDevEvent('state_changed', 'running');
          startDevTunnelStats();
        },
        DeleteProfile: asyncVoid,
        FetchSubscriptionStats: async (rawURL: unknown) => {
          const url = String(rawURL ?? '');
          markDevSubConnected(url, !!window.__pwdttDevConnected);
          return devFetchSubscriptionStats(url, !!window.__pwdttDevConnected);
        },
        FetchSubscriptionURL: async (rawURL: unknown) => devFetchSubscriptionURL(String(rawURL ?? '')),
        ParseWdttLink: async (link: unknown) => {
          const s = String(link ?? '').trim();
          const parsed = parseWdttFromSubBody(s);
          if (!parsed?.subUrl) throw new Error('wdtt link has no sub field');
          return devFetchSubscriptionURL(parsed.subUrl);
        },
        GetAutoStart: async () => false,
        GetProfile: async () => null,
        IsRunning: async () => !!window.__pwdttDevConnected,
        SaveProfile: asyncVoid,
        SetAutoStart: asyncVoid,
        SetTrayEnabled: asyncVoid,
        SetVKThroughTunnel: asyncVoid,
        GetVKThroughTunnel: async () => false,
        GetVKCookiesStatus: async () => {
          const useCookies = readVkUseCookies();
          const raw = readVkCookiesRaw();
          const hasCookies = !!raw.trim();
          const path = 'localStorage · pwdtt_vk_cookies_raw';
          if (!hasCookies) {
            return {
              ok: false,
              expired: false,
              useCookies,
              hint: 'Cookies не заданы — войдите через VK или вставьте вручную',
              path,
            };
          }
          const ok = vkCookiesHasRemix(raw);
          if (!ok) {
            return {
              ok: false,
              expired: true,
              useCookies,
              hint: 'remixsid не найден — проверьте формат cookies',
              path,
            };
          }
          return {
            ok: true,
            expired: false,
            useCookies,
            hint: useCookies ? 'Cookies действительны' : 'Cookies действительны (тумблер выключен)',
            path,
          };
        },
        CheckVKCookiesDraft: async (payload: unknown) => {
          const useCookies = readVkUseCookies();
          const raw = String(payload ?? '');
          const path = 'localStorage · pwdtt_vk_cookies_raw';
          if (!raw.trim()) {
            return {
              ok: false,
              expired: false,
              useCookies,
              hint: 'Cookies не заданы — войдите через VK или вставьте вручную',
              path,
            };
          }
          const ok = vkCookiesHasRemix(raw);
          if (!ok) {
            return {
              ok: false,
              expired: false,
              useCookies,
              hint: 'remixsid не найден — проверьте формат cookies',
              path,
            };
          }
          return {
            ok: true,
            expired: false,
            useCookies,
            hint: useCookies ? 'Cookies действительны' : 'Cookies действительны (тумблер выключен)',
            path,
          };
        },
        GetVKCookiesRaw: async () => readVkCookiesRaw(),
        GetVKUseCookies: async () => readVkUseCookies(),
        SetVKUseCookies: async (v: unknown) => {
          localStorage.setItem(VK_USE_COOKIES_KEY, v ? '1' : '0');
        },
        SaveVKCookies: async (raw: unknown) => {
          const s = String(raw ?? '').trim();
          if (!s) throw new Error('пустые cookies');
          localStorage.setItem(VK_COOKIES_RAW_KEY, s);
        },
        ClearVKCookies: async () => {
          localStorage.removeItem(VK_COOKIES_RAW_KEY);
        },
        GetAppVersion: async () => '0.3.90',
        GetDataDir: async () => '/tmp/wdtt-data',
        CheckForUpdate: async () => ({
          current: '0.3.90',
          latest: '0.3.90',
          hasUpdate: false,
          downloadURL: '',
          releaseURL: 'https://github.com/ildarmaga/pwdtt-client/releases',
          checkedAt: new Date().toISOString(),
        }),
        DownloadAndApplyUpdate: async () => {
          if (window.__pwdttDevConnected) {
            return { ok: false, message: 'Сначала отключитесь — нажмите кнопку питания на главном экране' };
          }
          for (let i = 0; i <= 100; i += 20) {
            emitDevEvent('update_progress', { phase: 'downloading', percent: i, message: `Скачивание… ${i}%` });
            await new Promise(r => setTimeout(r, 120));
          }
          return { ok: true, message: 'dev mock — обновление' };
        },
        CancelUpdateDownload: asyncVoid,
        StartVKLogin: async () => ({ url: '', active: true, native: true }),
        StopVKLogin: asyncVoid,
        PollVKLogin: async () => ({ done: false, status: 'waiting', message: 'dev mock' }),
      },
    },
  };

  seedDevSettings();
  console.info('[WDTT] browser dev — sub import, connect, metrics simulation');
}

export function installWailsDevMocks() {
  if (import.meta.env.PROD) return;
  if (window.go?.backend?.App) return;
  installRuntimeMock();
  installGoMock();
}

installWailsDevMocks();
