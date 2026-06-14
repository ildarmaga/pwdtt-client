const STORAGE_KEY = 'wdtt_selected_server';

type Listener = (id: string | null) => void;

let id: string | null = (() => {
  try {
    return localStorage.getItem(STORAGE_KEY);
  } catch {
    return null;
  }
})();

const listeners = new Set<Listener>();

/**
 * Глобальный стор выбранного сервера.
 * Хранит id выбранного профиля и переживает смену роута (Connect ↔ Logs):
 * страница Connect размонтируется при навигации, поэтому локальный useState
 * сбрасывался на первый сервер. Теперь выбор восстанавливается из этого стора.
 */
export const selectedServerStore = {
  getId: (): string | null => id,
  setId: (next: string | null) => {
    if (id === next) return;
    id = next;
    try {
      if (next) localStorage.setItem(STORAGE_KEY, next);
      else localStorage.removeItem(STORAGE_KEY);
    } catch {
      /* ignore */
    }
    listeners.forEach(fn => fn(id));
  },
  subscribe: (fn: Listener) => {
    listeners.add(fn);
    return () => { listeners.delete(fn); };
  },
};
