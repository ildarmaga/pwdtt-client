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

/** wdtt:// с полными параметрами подключения внутри base64 (ip+dtls+pass) — импорт без панели. */
function isOfflineWdtt(raw: string): boolean {
  const link = parseWdttFromSubBody(raw.trim());
  return Boolean(link && link.ip && link.dtlsPort && link.password);
}

export function isImportableInput(raw: string): boolean {
  const s = raw.trim();
  if (!s) return false;
  if (isPanelSubUrl(s)) return true;
  if (!s.startsWith('wdtt://')) return false;
  return isOfflineWdtt(s) || extractSubFromWdtt(s) !== null;
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

/**
 * Импорт сервера:
 *  - URL подписки панели → загрузка с панели (нужен интернет);
 *  - wdtt:// с полным набором (ip+dtls+pass) → офлайн из base64, панель опционально для статистики;
 *  - wdtt:// только с полем sub → загрузка с панели.
 */
export async function resolveWdttImport(raw: string): Promise<WdttLink | null> {
  const s = raw.trim();
  if (!s) return null;

  // 1. Голый URL подписки панели — поведение как раньше.
  if (isPanelSubUrl(s)) {
    try {
      return subResultToLink(await FetchSubscriptionURL(s.split('?')[0]));
    } catch {
      return null;
    }
  }

  if (!s.startsWith('wdtt://')) return null;

  const inline = parseWdttFromSubBody(s);
  const hasOfflineParams = Boolean(inline && inline.ip && inline.dtlsPort && inline.password);

  // 2. Полный wdtt:// — работаем офлайн из base64. Панель используем только для
  //    подтягивания статистики (необязательно), ошибка сети не мешает импорту.
  if (hasOfflineParams && inline) {
    if (inline.subUrl && isPanelSubUrl(inline.subUrl)) {
      try {
        const r = await FetchSubscriptionURL(inline.subUrl);
        const fromPanel = subResultToLink(r);
        // Приоритет — данные из ссылки (они «офлайн-истина»), статистика/имя — с панели.
        return {
          ...fromPanel,
          ip: inline.ip,
          dtlsPort: inline.dtlsPort,
          password: inline.password,
          hashes: inline.hashes.length > 0 ? inline.hashes : fromPanel.hashes,
          name: inline.name !== 'Server' ? inline.name : fromPanel.name,
          vpnName: inline.vpnName || fromPanel.vpnName,
          deviceId: inline.deviceId || fromPanel.deviceId,
          subUrl: inline.subUrl,
        };
      } catch {
        // Панель недоступна — отдаём то, что в ссылке.
      }
    }
    return inline;
  }

  // 3. wdtt:// только с sub — старый путь через панель.
  const sub = extractSubFromWdtt(s);
  if (!sub) return null;
  try {
    return subResultToLink(await FetchSubscriptionURL(sub));
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
