/** Browser-only Vite preview: stub Wails runtime when window.go is missing. */

import { parseWdttUrl } from '../utils/wdttLink';
import { DEFAULT_SETTINGS } from '../types';
import {
  devFetchSubscriptionStats,
  devFetchSubscriptionURL,
  devRegisterWdttLink,
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
        useGlobalHashes: true,
        hashes: ['demo_vk_hash', '', '', ''],
      }));
    }
  } catch { /* ignore */ }
}

function installGoMock() {
  if (window.go?.backend?.App) return;

  window.__pwdttDevConnected = false;

  window.go = {
    backend: {
      App: {
        CheckVPN: async () => '',
        Connect: async () => {
          emitDevEvent('state_changed', 'connecting');
          await new Promise(r => setTimeout(r, 900));
          window.__pwdttDevConnected = true;
          emitDevEvent('state_changed', 'running');
          emitDevEvent('log', 'INFO', '[dev] VPN подключён — опрос sub активен');
        },
        Disconnect: async () => {
          emitDevEvent('state_changed', 'disconnecting');
          await new Promise(r => setTimeout(r, 400));
          window.__pwdttDevConnected = false;
          emitDevEvent('state_changed', 'stopped');
          emitDevEvent('log', 'INFO', '[dev] VPN отключён');
        },
        DeleteProfile: asyncVoid,
        FetchSubscriptionStats: async (rawURL: unknown) => {
          const url = String(rawURL ?? '');
          markDevSubConnected(url, !!window.__pwdttDevConnected);
          return devFetchSubscriptionStats(url, !!window.__pwdttDevConnected);
        },
        FetchSubscriptionURL: async (rawURL: unknown) => devFetchSubscriptionURL(String(rawURL ?? '')),
        ParseWdttLink: async (link: unknown) => {
          const parsed = parseWdttUrl(String(link ?? ''));
          if (!parsed) throw new Error('invalid link');
          devRegisterWdttLink(parsed);
          return {
            ip: parsed.ip,
            dtlsPort: parsed.dtlsPort,
            password: parsed.password,
            name: parsed.name,
            hashes: parsed.hashes,
            subUrl: parsed.subUrl ?? '',
            stats: parsed.stats,
          };
        },
        GetAutoStart: async () => false,
        GetProfile: async () => null,
        IsRunning: async () => !!window.__pwdttDevConnected,
        SaveProfile: asyncVoid,
        SetAutoStart: asyncVoid,
        SetTrayEnabled: asyncVoid,
      },
    },
  };

  seedDevSettings();
  console.info('[PWDTT] browser dev — sub import, connect, metrics simulation');
}

export function installWailsDevMocks() {
  if (import.meta.env.PROD) return;
  if (window.go?.backend?.App) return;
  installRuntimeMock();
  installGoMock();
}

installWailsDevMocks();
