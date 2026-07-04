import { useEffect, useRef, useState } from 'react';
import { IconX } from '@tabler/icons-react';
import { StartVKLogin, StopVKLogin, PollVKLogin } from '../../wailsjs/go/backend/App';

interface Props {
  onClose: () => void;
  onDone: () => void;
}

export default function VKAuth({ onClose, onDone }: Props) {
  const [nativeWindow, setNativeWindow] = useState(false);
  const [url, setUrl] = useState('');
  const [status, setStatus] = useState('Загрузка…');
  const [error, setError] = useState('');
  const pollRef = useRef(0);
  const nativeRef = useRef(false);

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const res = await StartVKLogin();
        if (cancelled) return;
        if (res.native) {
          nativeRef.current = true;
          setNativeWindow(true);
        } else {
          setUrl(res.url);
          setStatus('Войдите в VK — cookies сохранятся автоматически');
        }
      } catch (e) {
        if (!cancelled) setError(String(e));
      }
    })();

    const poll = () => {
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
          window.clearInterval(pollRef.current);
          return;
        }
        if (res.message) setStatus(res.message);
      }).catch(e => setError(String(e)));
    };

    poll();
    pollRef.current = window.setInterval(poll, 800);

    return () => {
      cancelled = true;
      window.clearInterval(pollRef.current);
      // Native WebView2 runs in a separate process — do not kill it on React
      // remount (Strict Mode / parent re-render closes the login window early).
      if (!nativeRef.current) {
        void StopVKLogin();
      }
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
        .vk-auth-modal--native { height: auto; max-height: min(320px, calc(100vh - 24px)); }
        .vk-auth-head { display: flex; align-items: center; justify-content: space-between; padding: 12px 14px; border-bottom: 1px solid var(--border-2); }
        .vk-auth-title { font-size: 15px; font-weight: 600; color: var(--text); }
        .vk-auth-close { background: none; border: none; cursor: pointer; color: var(--text); padding: 4px; }
        .vk-auth-frame { flex: 1; border: none; width: 100%; background: #fff; }
        .vk-auth-native { flex: 1; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 12px; padding: 24px 20px; text-align: center; color: var(--text-2); font-size: 14px; line-height: 1.5; }
        .vk-auth-native-icon { font-size: 42px; opacity: 0.85; }
        .vk-auth-native-err { color: #ef4444; }
        .vk-auth-foot { padding: 8px 12px; font-size: 11px; color: var(--text-3); text-align: center; border-top: 1px solid var(--border-2); min-height: 32px; }
        .vk-auth-foot--err { color: #ef4444; }
      `}</style>
      <div className="vk-auth-overlay">
        <div className={`vk-auth-modal${nativeWindow ? ' vk-auth-modal--native' : ''}`}>
          <div className="vk-auth-head">
            <span className="vk-auth-title">Вход в VK</span>
            <button type="button" className="vk-auth-close" onClick={handleClose} aria-label="Закрыть">
              <IconX size={18} />
            </button>
          </div>
          {nativeWindow ? (
            <div className="vk-auth-native">
              <div className="vk-auth-native-icon">🪟</div>
              <p className={error ? 'vk-auth-native-err' : ''}>{error || status}</p>
              <p style={{ fontSize: 12, color: 'var(--text-3)' }}>
                На экране или в панели задач найдите окно «WDTT — вход VK». Не закрывайте его, пока не войдёте в аккаунт.
              </p>
            </div>
          ) : url ? (
            <iframe className="vk-auth-frame" src={url} title="VK login" />
          ) : (
            <div style={{ flex: 1, display: 'flex', alignItems: 'center', justifyContent: 'center', color: 'var(--text-3)' }}>
              {error || 'Запуск…'}
            </div>
          )}
          {!nativeWindow && <div className={`vk-auth-foot${error ? ' vk-auth-foot--err' : ''}`}>{error || status}</div>}
        </div>
      </div>
    </>
  );
}
