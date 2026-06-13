import { parseWdttUrl, decodeBase64Utf8, type TrafficStats, type WdttLink } from '../utils/wdttLink';
import { logDevMetric } from './devMetricsLog';

const STORE_KEY = 'pwdtt_dev_subs';

type StoredSub = {
  subUrl: string;
  link: WdttLink;
  stats: TrafficStats;
  connectedAt?: number;
};

function loadStore(): Record<string, StoredSub> {
  try {
    return JSON.parse(localStorage.getItem(STORE_KEY) ?? '{}') as Record<string, StoredSub>;
  } catch {
    return {};
  }
}

function saveStore(store: Record<string, StoredSub>) {
  localStorage.setItem(STORE_KEY, JSON.stringify(store));
}

function normSubUrl(url: string) {
  return url.trim().split('?')[0];
}

function parseUserInfo(header: string): Partial<TrafficStats> | null {
  if (!header.trim()) return null;
  const out: Partial<TrafficStats> = {};
  for (const part of header.split(';')) {
    const idx = part.indexOf('=');
    if (idx <= 0) continue;
    const key = part.slice(0, idx).trim().toLowerCase();
    const val = Number(part.slice(idx + 1).trim());
    if (Number.isNaN(val)) continue;
    if (key === 'upload') out.upload = val;
    if (key === 'download') out.download = val;
    if (key === 'total') out.total = val;
    if (key === 'expire') out.expire = val;
  }
  return out;
}

function decodeHeaderValue(v: string): string {
  v = v.trim();
  if (v.startsWith('base64:')) {
    try {
      return decodeBase64Utf8(v.slice(7));
    } catch {
      return '';
    }
  }
  return v;
}

function decodeSubBody(body: string): string {
  body = body.trim();
  if (body.startsWith('wdtt://')) return body;
  try {
    const dec = atob(body.replace(/\s/g, ''));
    if (dec.startsWith('wdtt://')) return dec;
  } catch { /* ignore */ }
  return body;
}

function toProxyUrl(url: string): string {
  return `/sub-fetch?u=${encodeURIComponent(url)}`;
}

async function fetchSubRaw(url: string, method: 'GET' | 'HEAD') {
  const fetchUrl = toProxyUrl(url);
  logDevMetric(method, url, `via ${fetchUrl}`, true);
  const resp = await fetch(fetchUrl, {
    method,
    headers: { Accept: 'text/plain', 'User-Agent': 'WDTT/1.0' },
  });
  if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
  return resp;
}

function statsFromHeaders(resp: Response): TrafficStats | null {
  const userinfo = resp.headers.get('Subscription-Userinfo') ?? resp.headers.get('subscription-userinfo') ?? '';
  const base = parseUserInfo(userinfo);
  if (!base) return null;
  const stats: TrafficStats = {
    upload: base.upload ?? 0,
    download: base.download ?? 0,
    total: base.total ?? 0,
    expire: base.expire ?? 0,
  };
  const title = decodeHeaderValue(resp.headers.get('Profile-Title') ?? '');
  const announce = decodeHeaderValue(resp.headers.get('Announce') ?? '');
  const support = resp.headers.get('Profile-Web-Page-Url') ?? '';
  const iv = resp.headers.get('Profile-Update-Interval') ?? '';
  if (title) stats.title = title;
  if (announce) stats.announce = announce;
  if (support) stats.supportUrl = support;
  if (iv) stats.updateInterval = Number(iv) || undefined;
  return stats;
}

function defaultStats(): TrafficStats {
  return {
    upload: 512_000_000,
    download: 1_024_000_000,
    total: 0,
    expire: Math.floor(Date.now() / 1000) + 86400 * 30,
    title: 'MAGIC VPN',
    announce: 'Самый лучший VPN',
    supportUrl: 'https://devgamemaga.mooo.com/',
    updateInterval: 12,
  };
}

function linkFromStats(subUrl: string, stats: TrafficStats, parsed?: WdttLink | null): WdttLink {
  if (parsed) {
    return {
      ...parsed,
      subUrl,
      stats,
      vpnName: parsed.vpnName || stats.title,
    };
  }
  return {
    ip: 'devgamemaga.mooo.com',
    dtlsPort: '56000',
    password: 'demo-pass',
    name: 'PC-ildar',
    vpnName: stats.title ?? 'MAGIC VPN',
    hashes: [],
    subUrl,
    stats,
  };
}

function bumpStats(stats: TrafficStats, connected: boolean): TrafficStats {
  if (!connected) return stats;
  const bump = 12_000_000 + Math.floor(Math.random() * 4_000_000);
  return {
    ...stats,
    download: stats.download + bump,
    upload: stats.upload + Math.floor(bump * 0.15),
  };
}

export function markDevSubConnected(subUrl: string, connected: boolean) {
  const key = normSubUrl(subUrl);
  const store = loadStore();
  if (!store[key]) return;
  store[key].connectedAt = connected ? Date.now() : undefined;
  saveStore(store);
}

export async function devFetchSubscriptionURL(rawURL: string) {
  const subUrl = normSubUrl(rawURL);
  const store = loadStore();

  try {
    const resp = await fetchSubRaw(subUrl, 'GET');
    const body = await resp.text();
    const stats = statsFromHeaders(resp) ?? defaultStats();
    const wdttLink = decodeSubBody(body);
    const parsed = parseWdttUrl(wdttLink);
    const link = linkFromStats(subUrl, stats, parsed);
    store[subUrl] = { subUrl, link, stats };
    saveStore(store);
    logDevMetric('GET', subUrl, `OK · ${link.name} · userinfo=${!!stats.title}`, true);
    return {
      ip: link.ip,
      dtlsPort: link.dtlsPort,
      password: link.password,
      name: link.name,
      vpnName: link.vpnName,
      hashes: link.hashes,
      subUrl,
      stats,
    };
  } catch (err) {
    logDevMetric('GET', subUrl, String(err), false);
    if (store[subUrl]) {
      const { link, stats } = store[subUrl];
      return {
        ip: link.ip,
        dtlsPort: link.dtlsPort,
        password: link.password,
        name: link.name,
        vpnName: link.vpnName,
        hashes: link.hashes,
        subUrl,
        stats,
      };
    }
    throw err;
  }
}

export async function devFetchSubscriptionStats(rawURL: string, connected: boolean) {
  const subUrl = normSubUrl(rawURL);
  const store = loadStore();

  try {
    let resp = await fetchSubRaw(subUrl, 'HEAD');
    let stats = statsFromHeaders(resp);
    if (!stats) {
      logDevMetric('GET', subUrl, 'fallback: no userinfo on HEAD', true);
      resp = await fetchSubRaw(subUrl, 'GET');
      stats = statsFromHeaders(resp);
    } else {
      logDevMetric('HEAD', subUrl, `userinfo upload=${stats.upload} download=${stats.download}`, true);
    }
    if (!stats && store[subUrl]) stats = { ...store[subUrl].stats };
    if (!stats) stats = defaultStats();
    stats = bumpStats(stats, connected);
    if (store[subUrl]) {
      store[subUrl].stats = stats;
      saveStore(store);
    }
    return stats;
  } catch (err) {
    logDevMetric('HEAD', subUrl, String(err), false);
    let stats = store[subUrl]?.stats ?? defaultStats();
    stats = bumpStats(stats, connected);
    if (store[subUrl]) {
      store[subUrl].stats = stats;
      saveStore(store);
    }
    return stats;
  }
}

export function devRegisterWdttLink(link: WdttLink) {
  if (!link.subUrl) return;
  const subUrl = normSubUrl(link.subUrl);
  const stats = link.stats ?? defaultStats();
  const store = loadStore();
  store[subUrl] = { subUrl, link: { ...link, subUrl, stats }, stats };
  saveStore(store);
  logDevMetric('IMPORT', subUrl, `wdtt:// + sub · ${link.name}`, true);
}
