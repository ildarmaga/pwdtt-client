import type { AppSettings, Server } from './types';

/** Хеши для подключения: подписка/профиль приоритетнее пустых глобальных. */
export function resolveConnectHashes(s: AppSettings, server: Server): string[] {
  const profile = (server.hashes ?? []).filter(h => h.trim());
  const global = s.hashes.filter(h => h.trim());
  if (server.linkManaged && profile.length > 0) return profile;
  if (s.useGlobalHashes) return global.length > 0 ? global : profile;
  return profile.length > 0 ? profile : global;
}

export function connectHashesHint(s: AppSettings, server: Server | null): string | null {
  if (!server) return null;
  const profile = (server.hashes ?? []).filter(h => h.trim()).length;
  const global = s.hashes.filter(h => h.trim()).length;
  if (s.useGlobalHashes && global === 0 && profile > 0) {
    return `Хеши из подписки (${profile}) — включите «Глобальные хеши» или они подхватятся автоматически`;
  }
  return null;
}
