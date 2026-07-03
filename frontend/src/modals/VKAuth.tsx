import { useEffect, useRef, useState } from 'react';
import { IconX } from '@tabler/icons-react';
import { StartVKLogin, StopVKLogin, PollVKLogin } from '../../wailsjs/go/backend/App';

interface Props {
  onClose: () => void;
  onDone: () => void;
}

export default function VKAuth({ onClose, onDone }: Props) {
  const [url, setUrl] = useState('');
  const [status, setStatus] = useState('Загрузка…');
  const [error, setError] = useState('');
  const pollRef = useRef(0);

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const res = await StartVKLogin();
        if (cancelled) return;
        setUrl(res.url);
        setStatus('Войдите в VK — cookies сохранятся автоматически');
      } catch (e) {
        if (!cancelled) setError(String(e));
      }
    })();

    pollRef.current = window.setInterval(() => {
      void PollVKLogin().then(res => {
        if (res.done) {
          setStatus(res.message || 'Готово');
          window.clearInterval(pollRef.current);
          onDone();
          onClose();
          return;
        }
        if (res.status === 'error') {
          setError(res.message || 'Ошибка');
          return;
        }
        if (res.message) setStatus(res.message);
      }).catch(e => setError(String(e)));
    }, 1200);

    return () => {
      cancelled = true;
      window.clearInterval(pollRef.current);
      void StopVKLogin();
    };
  }, [onClose, onDone]);

  const handleClose = () => {
    window.clearInterval(pollRef.current);
    void StopVKLogin();
    onClose();
  };

  return (
    <>
      <style>{`
        .vk-auth-overlay { position: fixed; inset: 0; background: var(--overlay-bg); backdrop-filter: blur(4px); z-index: 300; display: flex; align-items: center; justify-content: center; padding: 12px; }
        .vk-auth-modal { background: var(--surface); border-radius: 14px; width: min(520px, 100%); height: min(720px, calc(100vh - 24px)); display: flex; flex-direction: column; border: 1px solid var(--border); box-shadow: var(--shadow); overflow: hidden; }
        .vk-auth-head { display: flex; align-items: center; justify-content: space-between; padding: 12px 14px; border-bottom: 1px solid var(--border-2); }
        .vk-auth-title { font-size: 15px; font-weight: 600; color: var(--text); }
        .vk-auth-close { background: none; border: none; cursor: pointer; color: var(--text); padding: 4px; }
        .vk-auth-frame { flex: 1; border: none; width: 100%; background: #fff; }
        .vk-auth-foot { padding: 8px 12px; font-size: 11px; color: var(--text-3); text-align: center; border-top: 1px solid var(--border-2); min-height: 32px; }
        .vk-auth-foot--err { color: #ef4444; }
      `}</style>
      <div className="vk-auth-overlay">
        <div className="vk-auth-modal">
          <div className="vk-auth-head">
            <span className="vk-auth-title">Вход в VK</span>
            <button type="button" className="vk-auth-close" onClick={handleClose} aria-label="Закрыть">
              <IconX size={18} />
            </button>
          </div>
          {url ? (
            <iframe className="vk-auth-frame" src={url} title="VK login" />
          ) : (
            <div style={{ flex: 1, display: 'flex', alignItems: 'center', justifyContent: 'center', color: 'var(--text-3)' }}>
              {error || 'Запуск…'}
            </div>
          )}
          <div className={`vk-auth-foot${error ? ' vk-auth-foot--err' : ''}`}>{error || status}</div>
        </div>
      </div>
    </>
  );
}
