export interface TunnelStats {
  rxBytes: number;
  txBytes: number;
  rxRate: number;
  txRate: number;
  turnRttMs: number;
  dtlsHsMs: number;
  internetRttMs: number;
  workers: number;
  assignedWorkers: number;
  connectedAtMs: number;
}

type Listener = (stats: TunnelStats | null) => void;

let current: TunnelStats | null = null;
let prevRx = 0;
let prevTx = 0;
let prevAt = 0;
const listeners = new Set<Listener>();

function emit() {
  listeners.forEach(fn => fn(current));
}

export function formatRate(bps: number): string {
  if (!bps || bps <= 0) return '0 B/s';
  const units = ['B/s', 'KB/s', 'MB/s'];
  let v = bps;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  const num = i === 0 ? String(Math.round(v)) : v.toFixed(1).replace('.', ',');
  return `${num} ${units[i]}`;
}

export function formatMs(ms: number): string {
  if (!ms || ms <= 0) return '—';
  return `${Math.round(ms)} ms`;
}

export function formatDuration(totalSec: number): string {
  const sec = Math.max(0, Math.floor(totalSec));
  const h = Math.floor(sec / 3600);
  const m = Math.floor((sec % 3600) / 60);
  const s = sec % 60;
  if (h > 0) {
    return `${h}:${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`;
  }
  return `${m}:${String(s).padStart(2, '0')}`;
}

export const tunnelStatsStore = {
  subscribe(fn: Listener) {
    listeners.add(fn);
    fn(current);
    return () => { listeners.delete(fn); };
  },
  get: () => current,
  reset() {
    current = null;
    prevRx = 0;
    prevTx = 0;
    prevAt = 0;
    emit();
  },
  update(raw: {
    rxBytes: number;
    txBytes: number;
    workers: number;
    assignedWorkers: number;
    connectedAtMs: number;
    turnRttMs: number;
    dtlsHsMs: number;
    internetRttMs: number;
  }) {
    const now = Date.now();
    let rxRate = 0;
    let txRate = 0;
    if (prevAt > 0) {
      const dt = (now - prevAt) / 1000;
      if (dt > 0) {
        rxRate = Math.max(0, (raw.rxBytes - prevRx) / dt);
        txRate = Math.max(0, (raw.txBytes - prevTx) / dt);
      }
    }
    prevRx = raw.rxBytes;
    prevTx = raw.txBytes;
    prevAt = now;
    current = {
      rxBytes: raw.rxBytes,
      txBytes: raw.txBytes,
      rxRate,
      txRate,
      turnRttMs: raw.turnRttMs,
      dtlsHsMs: raw.dtlsHsMs,
      internetRttMs: raw.internetRttMs,
      workers: raw.workers,
      assignedWorkers: raw.assignedWorkers,
      connectedAtMs: raw.connectedAtMs,
    };
    emit();
  },
};
