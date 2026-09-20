export interface ExpireStats {
  expire: number;
  expireKind?: string;
  expireState?: string;
  remainingSeconds?: number;
  serverTs?: number;
  fetchedAt?: number;
}

function daysWord(n: number): string {
  const mod10 = n % 10;
  const mod100 = n % 100;
  if (mod100 >= 11 && mod100 <= 14) return 'дней';
  if (mod10 === 1) return 'день';
  if (mod10 >= 2 && mod10 <= 4) return 'дня';
  return 'дней';
}

function hoursWord(n: number): string {
  const mod10 = n % 10;
  const mod100 = n % 100;
  if (mod100 >= 11 && mod100 <= 14) return 'часов';
  if (mod10 === 1) return 'час';
  if (mod10 >= 2 && mod10 <= 4) return 'часа';
  return 'часов';
}

function minutesWord(n: number): string {
  const mod10 = n % 10;
  const mod100 = n % 100;
  if (mod100 >= 11 && mod100 <= 14) return 'минут';
  if (mod10 === 1) return 'минута';
  if (mod10 >= 2 && mod10 <= 4) return 'минуты';
  return 'минут';
}

export function formatRemainingDuration(seconds: number): string {
  const sec = Math.max(0, Math.floor(seconds));
  const days = Math.floor(sec / 86400);
  const hours = Math.floor((sec % 86400) / 3600);
  const minutes = Math.floor((sec % 3600) / 60);
  if (days > 0 && hours > 0) return `${days} ${daysWord(days)} ${hours} ${hoursWord(hours)}`;
  if (days > 0) return `${days} ${daysWord(days)}`;
  if (hours > 0 && minutes > 0) return `${hours} ${hoursWord(hours)} ${minutes} ${minutesWord(minutes)}`;
  if (hours > 0) return `${hours} ${hoursWord(hours)}`;
  if (minutes > 0) return `${minutes} ${minutesWord(minutes)}`;
  return 'меньше минуты';
}

function dateLabel(expireUnix: number): string {
  const d = new Date(expireUnix * 1000);
  const dd = String(d.getDate()).padStart(2, '0');
  const mm = String(d.getMonth() + 1).padStart(2, '0');
  const yyyy = d.getFullYear();
  return `${dd}.${mm}.${yyyy}`;
}

export function liveRemainingSeconds(stats: ExpireStats, nowMs = Date.now()): number {
  if (stats.expireState === 'pending' || stats.expireState === 'unlimited') return 0;
  if (stats.expireState === 'expired') return 0;
  const fetchedAt = stats.fetchedAt && stats.fetchedAt > 0 ? stats.fetchedAt : nowMs;
  const elapsed = Math.max(0, Math.floor((nowMs - fetchedAt) / 1000));
  if (stats.expire > 0 && stats.serverTs && stats.serverTs > 0) {
    const serverNow = stats.serverTs + elapsed;
    return Math.max(0, stats.expire - serverNow);
  }
  if (stats.remainingSeconds != null && stats.remainingSeconds >= 0) {
    return Math.max(0, stats.remainingSeconds - elapsed);
  }
  if (stats.expire > 0) {
    return Math.max(0, stats.expire - Math.floor(nowMs / 1000));
  }
  return 0;
}

export function expireLabel(stats: ExpireStats, nowMs = Date.now()): string {
  const kind = (stats.expireKind || '').toLowerCase();
  const state = (stats.expireState || '').toLowerCase();
  if (kind === 'promo') {
    if (state === 'pending') return 'Промо · срок начнётся при подключении';
    if (state === 'expired') return 'Срок устройства истёк';
    const left = liveRemainingSeconds(stats, nowMs);
    if (left <= 0) return 'Срок устройства истёк';
    return `Промо · устройству осталось: ${formatRemainingDuration(left)}`;
  }
  if (state === 'unlimited' || !stats.expire || stats.expire <= 0) return 'Бессрочно';
  const date = dateLabel(stats.expire);
  const left = liveRemainingSeconds(stats, nowMs);
  if (state === 'expired' || left <= 0) return `Истекло: ${date}`;
  const today = new Date(nowMs);
  today.setHours(0, 0, 0, 0);
  const expDay = new Date(stats.expire * 1000);
  expDay.setHours(0, 0, 0, 0);
  const daysLeft = Math.ceil((expDay.getTime() - today.getTime()) / 86400000);
  if (daysLeft === 0) return `Истекает сегодня · ${date}`;
  return `Осталось: ${daysLeft} ${daysWord(daysLeft)} · до ${date}`;
}

export function expireCompactLabel(stats: ExpireStats, nowMs = Date.now()): string {
  const kind = (stats.expireKind || '').toLowerCase();
  const state = (stats.expireState || '').toLowerCase();
  if (kind === 'promo') {
    if (state === 'pending') return 'промо';
    if (state === 'expired' || liveRemainingSeconds(stats, nowMs) <= 0) return 'истекло';
    return formatRemainingDuration(liveRemainingSeconds(stats, nowMs));
  }
  if (state === 'unlimited' || !stats.expire || stats.expire <= 0) return '∞';
  const left = liveRemainingSeconds(stats, nowMs);
  if (state === 'expired' || left <= 0) return 'истекло';
  const today = new Date(nowMs);
  today.setHours(0, 0, 0, 0);
  const expDay = new Date(stats.expire * 1000);
  expDay.setHours(0, 0, 0, 0);
  const daysLeft = Math.ceil((expDay.getTime() - today.getTime()) / 86400000);
  if (daysLeft === 0) return 'сегодня';
  return `${daysLeft} ${daysWord(daysLeft)}`;
}
