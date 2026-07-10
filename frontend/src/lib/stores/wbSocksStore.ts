/** Local SOCKS5 endpoint for v2rayN (WB socks-only mode). */

export type WBSocksEndpoint = {
  host: string;
  port: number;
  user: string;
  pass: string;
};

type Listener = (ep: WBSocksEndpoint | null) => void;

let endpoint: WBSocksEndpoint | null = null;
const listeners = new Set<Listener>();

function notify() {
  for (const l of listeners) l(endpoint);
}

export const wbSocksStore = {
  get(): WBSocksEndpoint | null {
    return endpoint;
  },
  set(host: string, port: number, user: string, pass: string) {
    if (!host || !port) {
      endpoint = null;
    } else {
      endpoint = { host, port, user: user || '', pass: pass || '' };
    }
    notify();
  },
  clear() {
    endpoint = null;
    notify();
  },
  subscribe(fn: Listener): () => void {
    listeners.add(fn);
    fn(endpoint);
    return () => { listeners.delete(fn); };
  },
  /** Fragment: «MAGIC VPN» + «ildar» → MAGIC_VPN-ildar */
  shareLabel(vpnName: string, userName: string): string {
    const vpn = vpnName.trim().replace(/\s+/g, '_');
    const user = userName.trim().replace(/\s+/g, '_');
    if (vpn && user && user.toLowerCase() !== vpn.toLowerCase()) {
      return `${vpn}-${user}`;
    }
    return vpn || user || 'WDTT';
  },
  /** socks://user:pass@host:port#MAGIC_VPN-ildar */
  url(ep: WBSocksEndpoint | null = endpoint, label = ''): string {
    if (!ep) return '';
    const auth = ep.user ? `${ep.user}:${ep.pass}@` : '';
    const base = `socks://${auth}${ep.host}:${ep.port}`;
    const tag = label.trim().replace(/\s+/g, '_');
    return tag ? `${base}#${tag}` : base;
  },
  /** tg://socks?server=&port=&user=&pass= — открыть прокси в Telegram */
  telegramUrl(ep: WBSocksEndpoint | null = endpoint): string {
    if (!ep) return '';
    const parts = [
      `server=${encodeURIComponent(ep.host)}`,
      `port=${ep.port}`,
    ];
    if (ep.user) parts.push(`user=${encodeURIComponent(ep.user)}`);
    if (ep.pass) parts.push(`pass=${encodeURIComponent(ep.pass)}`);
    return `tg://socks?${parts.join('&')}`;
  },
};
