import { useCallback, useEffect, useState } from 'react';
import { GetVKCookiesStatus, GetVKUseCookies } from '../../wailsjs/go/backend/App';
import type { backend } from '../../wailsjs/go/models';
import VKAuth from '../modals/VKAuth';

interface Props {
  locked?: boolean;
}

export default function VKAuthBar({ locked }: Props) {
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

  const modeLabel = (() => {
    if (!useCookies) return 'Гость';
    if (status?.ok) return 'Cookies';
    if (status?.expired) return 'Cookies · устарели';
    return 'Cookies · нет';
  })();

  const modeOk = useCookies && !!status?.ok;
  const modeBad = useCookies && (!!status?.expired || (status !== null && !status.ok));

  return (
    <>
      <style>{`
        .vk-auth-bar {
          display: flex; flex-direction: column; align-items: stretch; gap: 6px;
          width: 100%; margin-top: 2px;
        }
        .vk-auth-bar__row {
          display: flex; align-items: center; gap: 8px; min-width: 0;
        }
        .vk-auth-bar__mode {
          flex: 1; min-width: 0;
          font-size: 11px; font-weight: 600; letter-spacing: 0.03em;
          color: var(--text-2); white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
        }
        .vk-auth-bar__mode strong { color: var(--text); font-weight: 700; }
        .vk-auth-bar__mode--ok strong { color: #22c55e; }
        .vk-auth-bar__mode--bad strong { color: #ef4444; }
        .vk-auth-bar__btn {
          flex-shrink: 0;
          border: none; border-radius: 8px;
          padding: 6px 10px; font-size: 11px; font-weight: 650;
          background: #2787f5; color: #fff; cursor: pointer;
          white-space: nowrap;
        }
        .vk-auth-bar__btn:hover:not(:disabled) { filter: brightness(1.08); }
        .vk-auth-bar__btn:disabled { opacity: 0.45; cursor: not-allowed; }
        .vk-auth-bar__hint {
          font-size: 10px; color: var(--text-3); line-height: 1.3;
          overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
        }
        .vk-auth-bar__hint--ok { color: #22c55e; }
        .vk-auth-bar__hint--bad { color: #ef4444; }
      `}</style>
      <div className="vk-auth-bar">
        <div className="vk-auth-bar__row">
          <span
            className={`vk-auth-bar__mode${modeOk ? ' vk-auth-bar__mode--ok' : ''}${modeBad ? ' vk-auth-bar__mode--bad' : ''}`}
            title={status?.hint || modeLabel}
          >
            Режим: <strong>{modeLabel}</strong>
          </span>
          <button
            type="button"
            className="vk-auth-bar__btn"
            disabled={locked}
            onClick={() => setVkAuthOpen(true)}
          >
            Войти через VK
          </button>
        </div>
        {status?.hint && (
          <div
            className={`vk-auth-bar__hint${status.ok ? ' vk-auth-bar__hint--ok' : status.expired || !status.ok ? ' vk-auth-bar__hint--bad' : ''}`}
            title={status.hint}
          >
            {status.hint}
          </div>
        )}
      </div>
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
