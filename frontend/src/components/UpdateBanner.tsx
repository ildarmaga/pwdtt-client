import { useEffect, useState } from 'react';
import { IconDownload, IconX } from '@tabler/icons-react';
import { CheckForUpdate, DownloadAndApplyUpdate, GetUpdateDownloadState, IsUpdateDownloading } from '../../wailsjs/go/backend/App';
import type { backend } from '../../wailsjs/go/models';
import { updateStore } from '../lib/stores/updateStore';
import { tunnelStore } from '../lib/stores/tunnelStore';

const DISMISS_KEY = 'pwdtt_update_dismiss';

export default function UpdateBanner() {
  const [info, setInfo] = useState<backend.UpdateInfo | null>(null);
  const [dismissed, setDismissed] = useState(false);
  const [updateSnap, setUpdateSnap] = useState(() => updateStore.get());
  const [hint, setHint] = useState('');
  const [tunnelState, setTunnelState] = useState(() => tunnelStore.get());
  useEffect(() => tunnelStore.subscribe(setTunnelState), []);
  useEffect(() => updateStore.subscribe(setUpdateSnap), []);
  useEffect(() => {
    void (async () => {
      try {
        if (await IsUpdateDownloading()) {
          const st = await GetUpdateDownloadState();
          updateStore.syncFromBackend(st);
        }
      } catch { /* ignore */ }
    })();
  }, []);
  const locked = tunnelState === 'connected' || tunnelState === 'connecting' || tunnelState === 'disconnecting';
  const applying = updateSnap.phase === 'downloading' || updateSnap.phase === 'applying';
  const progress = updateSnap.percent;
  const progressMsg = updateSnap.message;

  useEffect(() => {
    void CheckForUpdate().then(res => {
      if (!res?.hasUpdate) return;
      try {
        const raw = localStorage.getItem(DISMISS_KEY);
        if (raw === res.latest) {
          setDismissed(true);
          return;
        }
      } catch { /* ignore */ }
      setInfo(res);
    }).catch(() => {});
  }, []);

  if (!info?.hasUpdate || dismissed) return null;

  const installUpdate = async () => {
    if (locked) {
      setHint('Сначала отключитесь — нажмите кнопку питания на главном экране');
      return;
    }
    if (updateStore.isActive()) {
      setHint('Обновление уже скачивается — откройте Настройки');
      return;
    }
    setHint('');
    updateStore.startDownload();
    try {
      const res = await DownloadAndApplyUpdate();
      if (!res.ok) {
        setHint(res.message || 'Ошибка обновления');
        updateStore.finish();
      }
    } catch (e) {
      setHint(String(e));
      updateStore.finish();
    }
  };

  const dismiss = () => {
    try { localStorage.setItem(DISMISS_KEY, info.latest || ''); } catch { /* ignore */ }
    setDismissed(true);
  };

  return (
    <>
      <style>{`
        .upd-banner { position: fixed; top: 10px; left: 50%; transform: translateX(-50%); z-index: 150; width: min(560px, calc(100vw - 90px)); background: #1677ff; color: #fff; border-radius: 12px; padding: 10px 12px; display: flex; flex-direction: column; gap: 8px; box-shadow: 0 8px 24px rgba(0,0,0,.25); }
        .upd-banner__row { display: flex; align-items: center; gap: 10px; }
        .upd-banner__text { flex: 1; font-size: 13px; line-height: 1.35; }
        .upd-banner__btn { border: none; border-radius: 8px; background: rgba(255,255,255,.18); color: #fff; font-size: 12px; font-weight: 600; padding: 7px 10px; cursor: pointer; display: inline-flex; align-items: center; gap: 6px; white-space: nowrap; }
        .upd-banner__btn:disabled { opacity: 0.7; cursor: default; }
        .upd-banner__close { border: none; background: transparent; color: #fff; opacity: .85; cursor: pointer; padding: 4px; }
        .upd-banner__hint { font-size: 12px; opacity: 0.95; line-height: 1.35; }
        .upd-banner__progress { height: 4px; border-radius: 2px; background: rgba(255,255,255,.25); overflow: hidden; }
        .upd-banner__progress-bar { height: 100%; background: #fff; transition: width 0.2s ease; }
      `}</style>
      <div className="upd-banner" role="status">
        <div className="upd-banner__row">
          <div className="upd-banner__text">
            Доступна новая версия <strong>{info.latest}</strong> (у вас {info.current})
          </div>
          <button type="button" className="upd-banner__btn" disabled={applying} onClick={installUpdate}>
            <IconDownload size={15} />
            {applying ? (progress > 0 ? `${progress}%` : '…') : 'Установить'}
          </button>
          <button type="button" className="upd-banner__close" aria-label="Скрыть" onClick={dismiss}>
            <IconX size={16} />
          </button>
        </div>
        {applying && (
          <>
            <div className="upd-banner__progress">
              <div className="upd-banner__progress-bar" style={{ width: `${Math.max(progress, 2)}%` }} />
            </div>
            {progressMsg && <div className="upd-banner__hint">{progressMsg}</div>}
          </>
        )}
        {!applying && hint && <div className="upd-banner__hint">{hint}</div>}
      </div>
    </>
  );
}
