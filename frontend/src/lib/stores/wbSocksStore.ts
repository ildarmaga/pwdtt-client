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
  /** socks5://user:pass@host:port — for paste into v2rayN / Clash */
  url(ep: WBSocksEndpoint | null = endpoint): string {
    if (!ep) return '';
    if (ep.user) {
      return `socks5://${encodeURIComponent(ep.user)}:${encodeURIComponent(ep.pass)}@${ep.host}:${ep.port}`;
    }
    return `socks5://${ep.host}:${ep.port}`;
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
