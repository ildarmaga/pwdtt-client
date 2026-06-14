/** ID сервера, к которому сейчас подключён (или идёт подключение) VPN. */
type Listener = (id: string | null) => void;

let id: string | null = null;
const listeners = new Set<Listener>();

export const activeServerStore = {
  getId: (): string | null => id,
  setId: (next: string | null) => {
    if (id === next) return;
    id = next;
    listeners.forEach(fn => fn(id));
  },
  subscribe: (fn: Listener) => {
    listeners.add(fn);
    fn(id);
    return () => { listeners.delete(fn); };
  },
};
