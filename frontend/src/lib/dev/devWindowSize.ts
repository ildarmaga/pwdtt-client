export type DevWindowSize = { width: number; height: number };

const STORAGE_KEY = 'pwdtt-dev-window-size';
export const DEFAULT_DEV_WINDOW: DevWindowSize = { width: 680, height: 700 };

type Listener = (size: DevWindowSize) => void;
const listeners = new Set<Listener>();

function load(): DevWindowSize {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return { ...DEFAULT_DEV_WINDOW };
    const parsed = JSON.parse(raw) as Partial<DevWindowSize>;
    const w = Number(parsed.width);
    const h = Number(parsed.height);
    if (w >= 320 && w <= 2400 && h >= 400 && h <= 1600) {
      return { width: Math.round(w), height: Math.round(h) };
    }
  } catch { /* ignore */ }
  return { ...DEFAULT_DEV_WINDOW };
}

let current = load();

function emit() {
  listeners.forEach(fn => fn(current));
}

export const devWindowSizeStore = {
  get: () => current,
  subscribe(fn: Listener) {
    listeners.add(fn);
    fn(current);
    return () => { listeners.delete(fn); };
  },
  set(width: number, height: number) {
    const w = Math.round(Math.min(2400, Math.max(320, width)));
    const h = Math.round(Math.min(1600, Math.max(400, height)));
    current = { width: w, height: h };
    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify(current));
    } catch { /* ignore */ }
    emit();
  },
  reset() {
    current = { ...DEFAULT_DEV_WINDOW };
    try {
      localStorage.removeItem(STORAGE_KEY);
    } catch { /* ignore */ }
    emit();
  },
};
