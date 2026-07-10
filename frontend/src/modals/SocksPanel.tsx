import { IconNetwork, IconBrandTelegram } from '@tabler/icons-react';
import { BrowserOpenURL } from '../../wailsjs/runtime/runtime';
import { wbSocksStore, type WBSocksEndpoint } from '../lib/stores/wbSocksStore';
import { toastStore } from '../lib/stores/toastStore';

interface Props {
  endpoint: WBSocksEndpoint;
  onClose: () => void;
}

export default function SocksPanel({ endpoint, onClose }: Props) {
  const copy = (text: string, ok: string) => {
    void navigator.clipboard?.writeText(text).then(
      () => toastStore.show(ok, 2500),
      () => toastStore.show(text, 5000),
    );
  };

  const openTelegram = () => {
    const url = wbSocksStore.telegramUrl(endpoint);
    if (!url) return;
    try {
      BrowserOpenURL(url);
      toastStore.show('Открываю Telegram…', 2000);
    } catch {
      copy(url, 'Ссылка tg:// скопирована');
    }
  };

  return (
    <>
      <style>{`
        .sk-overlay {
          position: fixed; inset: 0; background: var(--overlay-bg); backdrop-filter: blur(4px);
          display: flex; align-items: center; justify-content: center; padding: 16px; z-index: 100;
          animation: overlay-in 0.2s ease-out;
        }
        .sk-modal {
          background: var(--surface); border-radius: 12px; padding: 14px 16px;
          width: min(360px, calc(100vw - 24px)); box-shadow: var(--shadow);
          border: 1px solid var(--border); animation: modal-in 0.2s ease-out;
        }
        .sk-header { display: flex; align-items: center; gap: 8px; margin-bottom: 10px; color: var(--text); }
        .sk-title { font-size: 14px; font-weight: 600; flex: 1; }
        .sk-badge {
          font-size: 9px; font-weight: 700; letter-spacing: 0.05em; text-transform: uppercase;
          padding: 2px 7px; border-radius: 5px; background: color-mix(in srgb, var(--accent) 18%, transparent);
          color: var(--accent); border: 1px solid color-mix(in srgb, var(--accent) 35%, transparent);
        }
        .sk-close { background: none; border: none; cursor: pointer; font-size: 16px; color: var(--text-3); line-height: 1; padding: 0 2px; }
        .sk-close:hover { color: var(--text); }
        .sk-section { font-size: 10px; font-weight: 700; letter-spacing: 0.06em; text-transform: uppercase; color: var(--text-3); margin: 10px 0 2px; }
        .sk-section:first-of-type { margin-top: 0; }
        .sk-row {
          display: flex; align-items: center; justify-content: space-between; gap: 10px;
          padding: 6px 0; border-bottom: 1px solid var(--border-2); font-size: 12px; color: var(--text-3);
        }
        .sk-row:last-of-type { border-bottom: none; }
        .sk-row strong {
          color: var(--text); font-weight: 600; font-size: 12px;
          font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
          overflow: hidden; text-overflow: ellipsis; white-space: nowrap; max-width: 220px;
        }
        .sk-actions { display: flex; flex-direction: column; gap: 6px; margin-top: 10px; }
        .sk-btn {
          width: 100%; padding: 7px 10px; border-radius: 7px; border: 1px solid var(--border);
          background: var(--bg-2); color: var(--text); font-size: 12px; font-weight: 600;
          cursor: pointer; font-family: inherit; display: inline-flex; align-items: center;
          justify-content: center; gap: 6px;
        }
        .sk-btn:hover { border-color: var(--accent); color: var(--accent); }
        .sk-btn--primary { background: var(--accent); color: var(--accent-fg); border-color: transparent; }
        .sk-btn--primary:hover { filter: brightness(1.05); color: var(--accent-fg); }
        .sk-btn--tg { background: #2aabee; color: #fff; border-color: transparent; }
        .sk-btn--tg:hover { filter: brightness(1.06); color: #fff; border-color: transparent; }
      `}</style>
      <div className="sk-overlay" onClick={onClose}>
        <div className="sk-modal" onClick={e => e.stopPropagation()}>
          <div className="sk-header">
            <IconNetwork stroke={2} size={16} />
            <span className="sk-title">SOCKS5</span>
            <span className="sk-badge">WB</span>
            <button type="button" className="sk-close" onClick={onClose} aria-label="Закрыть">✕</button>
          </div>

          <div className="sk-section">Подключение</div>
          <div className="sk-row">
            <span>Адрес</span>
            <strong>{endpoint.host}:{endpoint.port}</strong>
          </div>
          <div className="sk-row">
            <span>Auth</span>
            {endpoint.user ? (
              <strong title={`${endpoint.user}:${endpoint.pass}`}>{endpoint.user} · ••••</strong>
            ) : (
              <strong>без пароля</strong>
            )}
          </div>

          <div className="sk-section">Действия</div>
          <div className="sk-actions">
            <button
              type="button"
              className="sk-btn sk-btn--primary"
              onClick={() => copy(wbSocksStore.url(endpoint), 'SOCKS URL скопирован')}
            >
              Копировать URL
            </button>
            <button
              type="button"
              className="sk-btn"
              onClick={() => copy(`${endpoint.host}:${endpoint.port}`, 'Адрес скопирован')}
            >
              Копировать адрес
            </button>
            <button
              type="button"
              className="sk-btn sk-btn--tg"
              title={wbSocksStore.telegramUrl(endpoint)}
              onClick={openTelegram}
            >
              <IconBrandTelegram size={14} stroke={1.8} />
              Открыть в Telegram
            </button>
          </div>
        </div>
      </div>
    </>
  );
}
