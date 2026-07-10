type Listener = () => void;

const listeners = new Set<Listener>();

export const settingsModalStore = {
  subscribe: (fn: Listener) => {
    listeners.add(fn);
    return () => { listeners.delete(fn); };
  },
  open: () => {
    listeners.forEach(fn => fn());
  },
};
