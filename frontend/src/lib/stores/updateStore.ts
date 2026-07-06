import type { UpdateProgressEvent } from '../types';

export type UpdatePhase = 'idle' | 'downloading' | 'applying' | 'error';

type Snapshot = {
  phase: UpdatePhase;
  percent: number;
  message: string;
};

type Listener = (snap: Snapshot) => void;

let phase: UpdatePhase = 'idle';
let percent = 0;
let message = '';

const listeners = new Set<Listener>();

function notify() {
  const snap = updateStore.get();
  listeners.forEach(fn => fn(snap));
}

export const updateStore = {
  get: (): Snapshot => ({ phase, percent, message }),

  isActive: () => phase === 'downloading' || phase === 'applying',

  /** Called from App-level EventsOn('update_progress') — survives modal unmount. */
  applyEvent: (p: UpdateProgressEvent | null | undefined) => {
    const evPhase = String(p?.phase ?? '');
    if (evPhase === 'downloading') {
      phase = 'downloading';
      percent = p?.percent ?? 0;
      message = p?.message ?? 'Скачивание…';
    } else if (evPhase === 'applying') {
      phase = 'applying';
      percent = p?.percent ?? 100;
      message = p?.message ?? 'Установка…';
    } else if (evPhase === 'error') {
      phase = 'error';
      message = p?.message ?? 'Ошибка';
    } else {
      return;
    }
    notify();
  },

  /** Sync from backend on Settings open (missed events while modal was closed). */
  syncFromBackend: (p: { phase?: string; percent?: number; message?: string } | null | undefined) => {
    if (!p?.phase || p.phase === 'idle') return;
    updateStore.applyEvent({
      phase: p.phase,
      percent: p.percent ?? 0,
      message: p.message ?? '',
    });
  },

  startDownload: () => {
    phase = 'downloading';
    percent = 0;
    message = 'Скачивание…';
    notify();
  },

  finish: () => {
    phase = 'idle';
    percent = 0;
    message = '';
    notify();
  },

  subscribe: (fn: Listener) => {
    listeners.add(fn);
    fn(updateStore.get());
    return () => { listeners.delete(fn); };
  },
};
