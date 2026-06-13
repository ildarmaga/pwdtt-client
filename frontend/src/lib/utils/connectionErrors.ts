/** Ответы сервера GETCONF → DENIED:* (см. wdtt/server.go, client/core/protocol.go) */
const SERVER_DENIED: Record<string, string> = {
  wrong_password: 'Неверный пароль VPN',
  expired: 'Срок подписки истёк',
  device_mismatch: 'Это устройство не привязано к паролю (лимит устройств)',
  deactivated: 'Пароль деактивирован администратором',
  too_many_sessions: 'Слишком много параллельных подключений с этого устройства',
  traffic_exceeded: 'Лимит трафика исчерпан',
};

const FATAL_PATTERNS: [RegExp, string][] = [
  [/неверный пароль/i, 'Неверный пароль VPN'],
  [/срок действия пароля истёк|истёк/i, 'Срок подписки истёк'],
  [/привязан к другому устройству|device_mismatch/i, 'Это устройство не привязано к паролю'],
  [/деактивирован|deactivated/i, 'Пароль деактивирован администратором'],
  [/too_many_sessions|слишком много.*сесс/i, 'Слишком много параллельных подключений'],
  [/traffic_exceeded|лимит трафика/i, 'Лимит трафика исчерпан'],
  [/WRAP_AUTH_TIMEOUT/i, 'TURN relay не ответил на DTLS (повтор подключения)'],
];

/** Преобразует сырое сообщение клиента/сервера в понятный текст для UI */
export function parseConnectionError(raw: string): string | null {
  const msg = raw.trim();
  if (!msg) return null;

  const denied = msg.match(/DENIED:([a-z_]+)/i);
  if (denied) {
    return SERVER_DENIED[denied[1].toLowerCase()] ?? `Сервер отклонил подключение (${denied[1]})`;
  }

  if (msg.includes('FATAL_AUTH')) {
    const tail = msg.replace(/^.*FATAL_AUTH:\s*/i, '').trim();
    for (const [re, text] of FATAL_PATTERNS) {
      if (re.test(tail) || re.test(msg)) return text;
    }
    const paren = tail.match(/доступ запрещён \(([^)]+)\)/i);
    if (paren) {
      const code = paren[1].toLowerCase();
      return SERVER_DENIED[code] ?? `Сервер отклонил подключение (${code})`;
    }
    return tail || 'Сервер отклонил подключение';
  }

  if (/Фатальная ошибка/i.test(msg)) {
    return parseConnectionError(msg.replace(/^.*Фатальная ошибка:\s*/i, ''));
  }

  for (const [re, text] of FATAL_PATTERNS) {
    if (re.test(msg)) return text;
  }

  return null;
}

export function serverDeniedReasons(): typeof SERVER_DENIED {
  return { ...SERVER_DENIED };
}

/** Примеры для dev-панели тестирования баннера ошибок */
export function connectionErrorSamples(): { label: string; raw: string }[] {
  const denied = Object.entries(SERVER_DENIED).map(([code, label]) => ({
    label,
    raw: `DENIED:${code}`,
  }));
  return [
    ...denied,
    { label: 'Таймаут DTLS', raw: 'FATAL_AUTH: WRAP_AUTH_TIMEOUT' },
    { label: 'Общая ошибка', raw: 'FATAL_AUTH: доступ запрещён (unknown_reason)' },
  ];
}
