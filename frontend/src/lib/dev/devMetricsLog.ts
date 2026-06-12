export type DevMetricEntry = {
  ts: number;
  method: string;
  url: string;
  detail: string;
  ok: boolean;
};

const listeners = new Set<(entries: DevMetricEntry[]) => void>();
let entries: DevMetricEntry[] = [];

export function logDevMetric(method: string, url: string, detail: string, ok = true) {
  entries = [{ ts: Date.now(), method, url, detail, ok }, ...entries].slice(0, 20);
  listeners.forEach(fn => fn(entries));
}

export function subscribeDevMetrics(fn: (entries: DevMetricEntry[]) => void) {
  listeners.add(fn);
  fn(entries);
  return () => listeners.delete(fn);
}
