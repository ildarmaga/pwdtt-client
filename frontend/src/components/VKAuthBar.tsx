import { useCallback, useEffect, useState } from 'react';
import { GetVKCookiesStatus, GetVKUseCookies } from '../../wailsjs/go/backend/App';
import type { backend } from '../../wailsjs/go/models';
import VKAuth from '../modals/VKAuth';

interface Props {
  locked?: boolean;
  /** Match SOCKS chip look in session-stats. */
  className?: string;
}

/** Compact chip like WB «SOCKS»: Cookies / Гость. Click → VK login. */
export default function VKAuthBar({ locked, className = 'socks-chip' }: Props) {
  const [vkAuthOpen, setVkAuthOpen] = useState(false);
  const [useCookies, setUseCookies] = useState(false);
  const [status, setStatus] = useState<backend.VKCookiesStatus | null>(null);

  const refresh = useCallback(() => {
    GetVKUseCookies().then(setUseCookies).catch(() => setUseCookies(false));
    GetVKCookiesStatus().then(setStatus).catch(() => setStatus(null));
  }, []);

  useEffect(() => {
    refresh();
    const id = window.setInterval(refresh, 8000);
    return () => window.clearInterval(id);
  }, [refresh]);

  const label = (() => {
    if (!useCookies) return 'Гость';
    if (status?.ok) return 'Cookies';
    if (status?.expired) return 'Cookies!';
    return 'Cookies?';
  })();

  const title = status?.hint
    || (!useCookies ? 'Режим гость — нажмите, чтобы войти через VK' : 'Режим cookies — нажмите, чтобы войти снова');

  const tone = !useCookies
    ? 'vk-mode-chip--guest'
    : status?.ok
      ? 'vk-mode-chip--ok'
      : 'vk-mode-chip--bad';

  return (
    <>
      <style>{`
        .vk-mode-chip--ok {
          border-color: color-mix(in srgb, #22c55e 45%, var(--border));
          background: color-mix(in srgb, #22c55e 16%, transparent);
          color: #4ade80;
        }
        .vk-mode-chip--ok:hover:not(:disabled) {
          background: #22c55e; color: #052e16; border-color: transparent;
        }
        .vk-mode-chip--guest {
          border-color: color-mix(in srgb, #2787f5 40%, var(--border));
          background: color-mix(in srgb, #2787f5 14%, transparent);
          color: #60a5fa;
        }
        .vk-mode-chip--guest:hover:not(:disabled) {
          background: #2787f5; color: #fff; border-color: transparent;
        }
        .vk-mode-chip--bad {
          border-color: color-mix(in srgb, #ef4444 45%, var(--border));
          background: color-mix(in srgb, #ef4444 14%, transparent);
          color: #f87171;
        }
        .vk-mode-chip--bad:hover:not(:disabled) {
          background: #ef4444; color: #fff; border-color: transparent;
        }
      `}</style>
      <button
        type="button"
        className={`${className} ${tone}`}
        title={title}
        disabled={locked}
        onClick={() => setVkAuthOpen(true)}
      >
        {label}
      </button>
      {vkAuthOpen && (
        <VKAuth
          onClose={() => setVkAuthOpen(false)}
          onDone={() => {
            setUseCookies(true);
            refresh();
          }}
        />
      )}
    </>
  );
}
