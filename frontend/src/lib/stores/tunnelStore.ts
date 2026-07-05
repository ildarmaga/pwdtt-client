import type { TunnelState } from '../types';

type Listener = (state: TunnelState) => void;

let state: TunnelState = 'idle';
/** Persists across page navigation — anchor for uptime when backend ms not yet sent. */
let connectingSinceMs: number | null = null;
const listeners = new Set<Listener>();

export const tunnelStore = {
  get: () => state,
  getConnectingSince: () => connectingSinceMs,
  set: (s: TunnelState) => {
    if (s === 'connecting' && state !== 'connecting') {
      connectingSinceMs = Date.now();
    } else if (s === 'idle') {
      connectingSinceMs = null;
    }
    state = s;
    listeners.forEach(fn => fn(state));
  },
  subscribe: (fn: Listener) => {
    listeners.add(fn);
    fn(state);
    return () => { listeners.delete(fn); };
  },
};
