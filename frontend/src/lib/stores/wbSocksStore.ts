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
  /** Fragment: «MAGIC VPN» + «ildar» → MAGIC_VPN-ildar (как iOS proxyFragment) */
  shareLabel(vpnName: string, userName: string): string {
    const vpn = vpnName.trim().replace(/\s+-\s+/g, '-').replace(/\s+/g, '_');
    const user = userName.trim().replace(/\s+-\s+/g, '-').replace(/\s+/g, '_');
    if (vpn && user && user.toLowerCase() !== vpn.toLowerCase()) {
      return `${vpn}-${user}`;
    }
    return vpn || user || 'WDTT';
  },
  /**
   * Как iOS v2rayProxyUri / Happ:
   * socks://BASE64(user:pass)@127.0.0.1:10809#MAGIC_VPN-ildar
   */
  url(ep: WBSocksEndpoint | null = endpoint, label = ''): string {
    if (!ep) return '';
    let auth = '';
    if (ep.user) {
      const creds = `${ep.user}:${ep.pass}`;
      auth = `${btoa(creds)}@`;
    }
    const base = `socks://${auth}${ep.host}:${ep.port}`;
    const tag = label.trim().replace(/\s+-\s+/g, '-').replace(/\s+/g, '_');
    return tag ? `${base}#${tag}` : base;
  },
  /** tg://socks?server=&port=&user=&pass= — как iOS openTelegramProxy */
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
