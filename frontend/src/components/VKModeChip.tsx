import { useCallback, useEffect, useState } from 'react';
import { GetVKCookiesStatus, GetVKUseCookies } from '../../wailsjs/go/backend/App';
import type { backend } from '../../wailsjs/go/models';
import { vkModeStore } from '../lib/stores/vkModeStore';

/** Non-clickable mode plaque (like SOCKS): Cookies / Гость. */
export default function VKModeChip() {
  const [useCookies, setUseCookies] = useState(false);
  const [status, setStatus] = useState<backend.VKCookiesStatus | null>(null);

  const refresh = useCallback(() => {
    GetVKUseCookies().then(setUseCookies).catch(() => setUseCookies(false));
    GetVKCookiesStatus().then(setStatus).catch(() => setStatus(null));
  }, []);

  useEffect(() => {
    refresh();
    const off = vkModeStore.subscribe(refresh);
    const id = window.setInterval(refresh, 8000);
    return () => {
      off();
      window.clearInterval(id);
    };
  }, [refresh]);

  const label = (() => {
    if (!useCookies) return 'Гость';
    if (status?.ok) return 'Cookies';
    if (status?.expired) return 'Cookies!';
    return 'Cookies?';
  })();

  const title = status?.hint
    || (!useCookies ? 'Режим: гость' : 'Режим: cookies');

  const tone = !useCookies
    ? 'vk-mode-chip--guest'
    : status?.ok
      ? 'vk-mode-chip--ok'
      : 'vk-mode-chip--bad';

  return (
    <>
      <style>{`
        .vk-mode-chip {
          pointer-events: none;
          flex-shrink: 0;
          padding: 2px 8px;
          border-radius: 5px;
          font-size: 9px;
          font-weight: 700;
          letter-spacing: 0.05em;
          text-transform: uppercase;
          font-family: inherit;
          line-height: 1.4;
          border: 1px solid var(--border);
          user-select: none;
        }
        .vk-mode-chip--ok {
          border-color: color-mix(in srgb, #22c55e 45%, var(--border));
          background: color-mix(in srgb, #22c55e 16%, transparent);
          color: #4ade80;
        }
        .vk-mode-chip--guest {
          border-color: color-mix(in srgb, #2787f5 40%, var(--border));
          background: color-mix(in srgb, #2787f5 14%, transparent);
          color: #60a5fa;
        }
        .vk-mode-chip--bad {
          border-color: color-mix(in srgb, #ef4444 45%, var(--border));
          background: color-mix(in srgb, #ef4444 14%, transparent);
          color: #f87171;
        }
      `}</style>
      <span className={`vk-mode-chip ${tone}`} title={title}>{label}</span>
    </>
  );
}
