import { parseConnectionError } from '../utils/connectionErrors';

type Listener = (err: ConnectionError | null) => void;

export type ConnectionErrorKind = 'connect' | 'degraded';

export type ConnectionError = {
  message: string;
  at: number;
  kind: ConnectionErrorKind;
};

let current: ConnectionError | null = null;
let timer: ReturnType<typeof setTimeout> | null = null;
const listeners = new Set<Listener>();

/** Баннер ошибки подключения */
const BANNER_MS = 30_000;
/** Баннер деградации туннеля — дольше, есть кнопка Reconnect */
const DEGRADED_MS = 120_000;

function notify() {
  listeners.forEach(fn => fn(current));
}

export const connectionErrorStore = {
  subscribe(fn: Listener) {
    listeners.add(fn);
    fn(current);
    return () => { listeners.delete(fn); };
  },

  get(): ConnectionError | null {
    return current;
  },

  show(raw: string, ms = BANNER_MS) {
    const message = parseConnectionError(raw) ?? raw.trim();
    if (!message) return;
    if (timer) clearTimeout(timer);
    current = { message, at: Date.now(), kind: 'connect' };
    notify();
    timer = setTimeout(() => {
      current = null;
      notify();
    }, ms);
  },

  showDegraded(message: string, ms = DEGRADED_MS) {
    const text = message.trim();
    if (!text) return;
    if (timer) clearTimeout(timer);
    current = { message: text, at: Date.now(), kind: 'degraded' };
    notify();
    timer = setTimeout(() => {
      current = null;
      notify();
    }, ms);
  },

  dismiss() {
    if (timer) clearTimeout(timer);
    timer = null;
    current = null;
    notify();
  },

  clear() {
    this.dismiss();
  },
};
