import type { TrafficStats } from '../utils/wdttLink';

const STORAGE_KEY = 'wdtt_traffic_cache';

type CacheMap = Record<string, TrafficStats>;

function load(): CacheMap {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return {};
    const parsed = JSON.parse(raw);
    return parsed && typeof parsed === 'object' ? (parsed as CacheMap) : {};
  } catch {
    return {};
  }
}

let cache: CacheMap = load();

function persist() {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(cache));
  } catch { /* ignore */ }
}

/**
 * Кэш последних метрик подписки по subUrl.
 * Позволяет сохранять последнее значение при переходах между страницами
 * и показывать его сразу, не дожидаясь нового запроса.
 */
export const trafficStatsStore = {
  get(subUrl?: string | null): TrafficStats | null {
    if (!subUrl) return null;
    return cache[subUrl] ?? null;
  },
  set(subUrl: string | null | undefined, stats: TrafficStats) {
    if (!subUrl) return;
    cache = { ...cache, [subUrl]: stats };
    persist();
  },
  clear(subUrl: string) {
    if (!cache[subUrl]) return;
    const next = { ...cache };
    delete next[subUrl];
    cache = next;
    persist();
  },
};
