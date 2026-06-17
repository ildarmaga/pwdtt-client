import {
  FetchSubscriptionStats,
  FetchSubscriptionURL,
} from '../../../wailsjs/go/backend/App';
import type { backend } from '../../../wailsjs/go/models';
import type { Server } from '../types';

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
  vpnName?: string;
  subUrl?: string;
  deviceId?: string;
  stats?: TrafficStats;
}

/** Ссылка подписки WDTT-панели: https://host[:port]/любой/путь/TOKEN */
export function isPanelSubUrl(raw: string): boolean {
  try {
    const u = new URL(raw.trim().split('?')[0]);
    if (u.protocol !== 'http:' && u.protocol !== 'https:') return false;
    if (!u.hostname) return false;
    const parts = u.pathname.replace(/\/+$/, '').split('/').filter(Boolean);
    if (parts.length < 1) return false;
    const token = parts[parts.length - 1];
    return /^[a-z0-9]{8,32}$/.test(token);
  } catch {
    return false;
  }
}

function extractSubFromWdtt(raw: string): string | null {
  if (!raw.trim().startsWith('wdtt://')) return null;
  const link = parseWdttFromSubBody(raw.trim());
  if (!link?.subUrl || !isPanelSubUrl(link.subUrl)) return null;
  return link.subUrl;
}

export function isImportableInput(raw: string): boolean {
  const s = raw.trim();
  if (!s) return false;
  if (isPanelSubUrl(s)) return true;
  return extractSubFromWdtt(s) !== null;
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
  const vpnName = r.vpnName || stats?.title || undefined;
  const name = r.name || 'Server';
  return {
    ip: r.ip,
    dtlsPort: r.dtlsPort,
    password: r.password,
    name,
    vpnName,
    hashes: r.hashes ?? [],
    subUrl: r.subUrl || undefined,
    deviceId: r.deviceId || undefined,
    stats,
  };
}

/** Заголовок в статус-баре: «MAGIC VPN - ildar» (vpn + комментарий) */
export function serverVpnTitle(server: Server | null, traffic?: TrafficStats | null): string {
  if (!server) return 'Нет серверов';
  const vpn = (server.vpnName || traffic?.title || '').trim();
  const user = (server.name || '').trim();
  if (vpn && user && user.toLowerCase() !== vpn.toLowerCase()) {
    return `${vpn} - ${user}`;
  }
  return vpn || user || 'Server';
}

export async function fetchTrafficStats(subUrl: string): Promise<TrafficStats | null> {
  if (!isPanelSubUrl(subUrl)) return null;
  try {
    const r = await FetchSubscriptionStats(subUrl.trim());
    if (!r) return null;
    return statsFromResult(r);
  } catch {
    return null;
  }
}

/** Импорт: URL подписки панели или wdtt:// с полем sub внутри. */
export async function resolveWdttImport(raw: string): Promise<WdttLink | null> {
  const s = raw.trim();
  if (!s) return null;
  const sub = extractSubFromWdtt(s) || (isPanelSubUrl(s) ? s.split('?')[0] : null);
  if (!sub) return null;
  try {
    const r = await FetchSubscriptionURL(sub);
    return subResultToLink(r);
  } catch {
    return null;
  }
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

export function expireCompactLabel(stats: TrafficStats): string {
  if (!stats.expire || stats.expire <= 0) return '∞';
  const d = new Date(stats.expire * 1000);
  const today = new Date();
  today.setHours(0, 0, 0, 0);
  const expDay = new Date(d);
  expDay.setHours(0, 0, 0, 0);
  const daysLeft = Math.ceil((expDay.getTime() - today.getTime()) / 86400000);
  if (daysLeft < 0) return 'истекло';
  if (daysLeft === 0) return 'сегодня';
  return `${daysLeft} ${daysWord(daysLeft)}`;
}

export function subRefreshMs(stats: TrafficStats | null): number {
  const hours = stats?.updateInterval;
  if (hours && hours > 0) return hours * 3600_000;
  return 30 * 60_000;
}

/** Интервал опроса sub: userSec=0 → авто каждые 5 сек. */
export function metricsRefreshMs(_stats: TrafficStats | null, userSec: number): number {
  if (userSec > 0) return userSec * 1000;
  return 5_000;
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

/** Внутренний парсер wdtt:// из тела ответа подписки (не для ввода пользователем). */
export function parseWdttFromSubBody(raw: string): WdttLink | null {
  try {
    let str = raw.trim();
    if (!str.startsWith('wdtt://')) return null;
    const hashIdx = str.indexOf('#');
    let name = 'Server';
    if (hashIdx !== -1) {
      const n = str.slice(hashIdx + 1).trim();
      if (n) name = n;
      str = str.slice(0, hashIdx);
    }
    const payload = str.slice(7);
    const json = JSON.parse(decodeBase64Utf8(payload));
    const ip = String(json.ip ?? json.add ?? '').trim();
    const pass = String(json.pass ?? json.id ?? '').trim();
    const dtls = json.dtls ?? json.Dtls;
    const dtlsPort = dtls != null ? String(dtls) : '';
    const userName = String(json.name ?? json.ps ?? json.remark ?? json.email ?? name).trim() || name;
    const vpnName = String(json.vpn ?? json.VPN ?? '').trim() || undefined;
    const hashRaw = json.hash ?? json.vk_hash ?? '';
    const hashes = typeof hashRaw === 'string'
      ? hashRaw.split(',').map((h: string) => h.trim()).filter(Boolean)
      : [];
    const deviceId = String(json.did ?? json.device_id ?? '').trim() || undefined;
    const subRaw = String(json.sub ?? json.subUrl ?? json.sub_url ?? '').trim();
    const subUrl = subRaw && /^https?:\/\//i.test(subRaw) ? subRaw.split('?')[0] : undefined;
    if (!ip || !dtlsPort || !pass) return null;
    return { ip, dtlsPort, password: pass, hashes, name: userName, vpnName, deviceId, subUrl };
  } catch {
    return null;
  }
}

export function decodeBase64Utf8(b64: string): string {
  const bin = atob(b64.replace(/\s/g, ''));
  const bytes = Uint8Array.from(bin, c => c.charCodeAt(0));
  return new TextDecoder().decode(bytes);
}

type Listener = (link: WdttLink | null) => void;
let pending: WdttLink | null = null;
const listeners = new Set<Listener>();

export const wdttLinkStore = {
  subscribe: (fn: Listener) => { listeners.add(fn); fn(pending); return () => { listeners.delete(fn); }; },
  set: (link: WdttLink | null) => { pending = link; listeners.forEach(fn => fn(link)); },
  consume: () => { const l = pending; pending = null; listeners.forEach(fn => fn(null)); return l; },
};
