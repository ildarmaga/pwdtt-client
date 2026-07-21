/** Bump after VK login so the Connect mode chip refreshes immediately. */
type Listener = () => void;

const listeners = new Set<Listener>();
let rev = 0;

export const vkModeStore = {
  subscribe(fn: Listener) {
    listeners.add(fn);
    return () => { listeners.delete(fn); };
  },
  notify() {
    rev += 1;
    listeners.forEach(fn => fn());
  },
  rev() {
    return rev;
  },
};
