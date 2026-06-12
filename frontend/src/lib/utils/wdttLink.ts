import {
  FetchSubscriptionStats,
  FetchSubscriptionURL,
  ParseWdttLink,
} from '../../../wailsjs/go/backend/App';
import type { backend } from '../../../wailsjs/go/models';

export interface TrafficStats {
  upload: number;
  download: number;
  total: number;
  expire: number;
  title?: string;
  announce?: string;
  supportUrl?: string;
  updateInterval?: number;
}

export interface WdttLink {
  ip: string;
  dtlsPort: string;
  password: string;
  hashes: string[];
  name: string;
  subUrl?: string;
  stats?: TrafficStats;
}

export function isHttpUrl(raw: string): boolean {
  return /^https?:\/\//i.test(raw.trim());
}

export function isImportableInput(raw: string): boolean {
  const s = raw.trim();
  if (!s) return false;
  if (s.startsWith('wdtt://')) return true;
  return isHttpUrl(s);
}

function decodeBase64Utf8(b64: string): string {
  const bin = atob(b64.replace(/\s/g, ''));
  const bytes = Uint8Array.from(bin, c => c.charCodeAt(0));
  return new TextDecoder().decode(bytes);
}

function parseColonWdtt(stripped: string, name: string): WdttLink | null {
  const parts = stripped.split(':');
  if (parts.length < 5) return null;
  const ip = parts[0];
  const dtlsPort = parts[1];
  const password = parts[4];
  const hashes = parts[5]
    ? parts[5].split(',').map(h => h.trim()).filter(Boolean)
    : [];
  if (!ip || !dtlsPort || !password) return null;
  return { ip, dtlsPort, password, hashes, name };
}

function parseJsonWdtt(stripped: string): WdttLink | null {
  try {
    const json = JSON.parse(decodeBase64Utf8(stripped));
    const ip = String(json.ip ?? json.add ?? '').trim();
    const pass = String(json.pass ?? json.id ?? '').trim();
    const dtls = json.dtls ?? json.Dtls;
    const dtlsPort = dtls != null ? String(dtls) : '';
    const name = String(json.ps ?? json.remark ?? json.email ?? 'Server').trim() || 'Server';
    const hashRaw = json.hash ?? json.vk_hash ?? '';
    const hashes = typeof hashRaw === 'string'
      ? hashRaw.split(',').map((h: string) => h.trim()).filter(Boolean)
      : [];
    const subRaw = String(json.sub ?? json.subUrl ?? json.sub_url ?? '').trim();
    const subUrl = subRaw && /^https?:\/\//i.test(subRaw) ? subRaw.split('?')[0] : undefined;
    if (!ip || !dtlsPort || !pass) return null;
    return { ip, dtlsPort, password: pass, hashes, name, subUrl };
  } catch {
    return null;
  }
}

function looksLikeColonFormat(stripped: string): boolean {
  const parts = stripped.split(':');
  return parts.length >= 5 && !stripped.includes('=') && /^\d+$/.test(parts[1] ?? '');
}

function statsFromResult(r: backend.SubTrafficStats): TrafficStats {
  return {
    upload: r.upload,
    download: r.download,
    total: r.total,
    expire: r.expire,
    title: r.title || undefined,
    announce: r.announce || undefined,
    supportUrl: r.supportUrl || undefined,
    updateInterval: r.updateInterval || undefined,
  };
}

function subResultToLink(r: backend.SubImportResult): WdttLink {
  const stats = r.stats ? statsFromResult(r.stats) : undefined;
  const name = stats?.title || r.name || 'Server';
  return {
    ip: r.ip,
    dtlsPort: r.dtlsPort,
    password: r.password,
    name,
    hashes: r.hashes ?? [],
    subUrl: r.subUrl || undefined,
    stats,
  };
}

// wdtt:// — colon-формат или base64(JSON), fallback для dev/preview
export function parseWdttUrl(raw: string): WdttLink | null {
  try {
    let str = raw.trim();
    let name = 'Server';
    const hashIdx = str.indexOf('#');
    if (hashIdx !== -1) {
      const candidate = str.slice(hashIdx + 1).trim();
      if (candidate) name = candidate;
      str = str.slice(0, hashIdx);
    }
    const stripped = str.replace(/^wdtt:\/\//, '');
    if (!stripped) return null;
    if (looksLikeColonFormat(stripped)) {
      return parseColonWdtt(stripped, name);
    }
    return parseJsonWdtt(stripped) ?? parseColonWdtt(stripped, name);
  } catch {
    return null;
  }
}

export async function fetchTrafficStats(subUrl: string): Promise<TrafficStats | null> {
  try {
    const r = await FetchSubscriptionStats(subUrl.trim());
    if (!r) return null;
    return statsFromResult(r);
  } catch {
    return null;
  }
}

export async function resolveWdttImport(raw: string): Promise<WdttLink | null> {
  const s = raw.trim();
  if (!s) return null;
  try {
    if (isHttpUrl(s)) {
      const r = await FetchSubscriptionURL(s);
      return subResultToLink(r);
    }
    if (s.startsWith('wdtt://')) {
      try {
        const r = await ParseWdttLink(s);
        return subResultToLink(r);
      } catch {
        return parseWdttUrl(s);
      }
    }
  } catch {
    return null;
  }
  return null;
}

export function formatBytes(n: number): string {
  if (!n || n <= 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let v = n;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  const numStr = i === 0 ? String(v) : v.toFixed(i >= 2 ? 2 : 1);
  return `${numStr.replace('.', ',')} ${units[i]}`;
}

export function trafficCompactLabel(stats: TrafficStats): string {
  const used = stats.upload + stats.download;
  const totalStr = !stats.total || stats.total <= 0 ? '∞' : formatBytes(stats.total).replace(' ', '');
  return `${formatBytes(used).replace(' ', '')}/${totalStr}`;
}

function daysWord(n: number): string {
  const mod10 = n % 10;
  const mod100 = n % 100;
  if (mod100 >= 11 && mod100 <= 14) return 'дней';
  if (mod10 === 1) return 'день';
  if (mod10 >= 2 && mod10 <= 4) return 'дня';
  return 'дней';
}

export function expireLabel(stats: TrafficStats): string {
  if (!stats.expire || stats.expire <= 0) return 'Бессрочно';
  const d = new Date(stats.expire * 1000);
  const dd = String(d.getDate()).padStart(2, '0');
  const mm = String(d.getMonth() + 1).padStart(2, '0');
  const yyyy = d.getFullYear();
  const today = new Date();
  today.setHours(0, 0, 0, 0);
  const expDay = new Date(d);
  expDay.setHours(0, 0, 0, 0);
  const daysLeft = Math.ceil((expDay.getTime() - today.getTime()) / 86400000);
  if (daysLeft < 0) return `Истекло: ${dd}.${mm}.${yyyy}`;
  if (daysLeft === 0) return `Истекает сегодня · ${dd}.${mm}.${yyyy}`;
  return `Истекает: ${dd}.${mm}.${yyyy} · осталось ${daysLeft} ${daysWord(daysLeft)}`;
}

export function subRefreshMs(stats: TrafficStats | null): number {
  const hours = stats?.updateInterval;
  if (hours && hours > 0) return hours * 3600_000;
  return 30 * 60_000;
}

export function trafficRemainLabel(stats: TrafficStats): string {
  if (!stats.total || stats.total <= 0) return '∞';
  const used = stats.upload + stats.download;
  const left = Math.max(0, stats.total - used);
  return formatBytes(left);
}

export function trafficUsedPercent(stats: TrafficStats): number | null {
  if (!stats.total || stats.total <= 0) return null;
  const used = stats.upload + stats.download;
  return Math.min(100, Math.max(0, (used / stats.total) * 100));
}

export function trafficFillColor(pct: number): string {
  if (pct >= 95) return '#ef4444';
  if (pct >= 80) return '#f59e0b';
  return 'var(--accent)';
}

type Listener = (link: WdttLink | null) => void;
let pending: WdttLink | null = null;
const listeners = new Set<Listener>();

export const wdttLinkStore = {
  subscribe: (fn: Listener) => { listeners.add(fn); fn(pending); return () => { listeners.delete(fn); }; },
  set: (link: WdttLink | null) => { pending = link; listeners.forEach(fn => fn(link)); },
  consume: () => { const l = pending; pending = null; listeners.forEach(fn => fn(null)); return l; },
};
