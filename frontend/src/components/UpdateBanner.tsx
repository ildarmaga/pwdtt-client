import { useEffect, useState } from 'react';
import { IconDownload, IconX } from '@tabler/icons-react';
import { CheckForUpdate, DownloadAndApplyUpdate } from '../../wailsjs/go/backend/App';
import type { backend } from '../../wailsjs/go/models';
import { tunnelStore } from '../lib/stores/tunnelStore';

const DISMISS_KEY = 'pwdtt_update_dismiss';

export default function UpdateBanner() {
  const [info, setInfo] = useState<backend.UpdateInfo | null>(null);
  const [dismissed, setDismissed] = useState(false);
  const [applying, setApplying] = useState(false);
  const [tunnelState, setTunnelState] = useState(() => tunnelStore.get());
  useEffect(() => tunnelStore.subscribe(setTunnelState), []);
  const locked = tunnelState === 'connected' || tunnelState === 'connecting';

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
    if (locked) return;
    setApplying(true);
    try {
      const res = await DownloadAndApplyUpdate();
      if (!res.ok && res.message) alert(res.message);
    } catch (e) {
      alert(String(e));
    } finally {
      setApplying(false);
    }
  };

  const dismiss = () => {
    try { localStorage.setItem(DISMISS_KEY, info.latest || ''); } catch { /* ignore */ }
    setDismissed(true);
  };

  return (
    <>
      <style>{`
        .upd-banner { position: fixed; top: 10px; left: 50%; transform: translateX(-50%); z-index: 150; width: min(560px, calc(100vw - 90px)); background: #1677ff; color: #fff; border-radius: 12px; padding: 10px 12px; display: flex; align-items: center; gap: 10px; box-shadow: 0 8px 24px rgba(0,0,0,.25); }
        .upd-banner__text { flex: 1; font-size: 13px; line-height: 1.35; }
        .upd-banner__btn { border: none; border-radius: 8px; background: rgba(255,255,255,.18); color: #fff; font-size: 12px; font-weight: 600; padding: 7px 10px; cursor: pointer; display: inline-flex; align-items: center; gap: 6px; white-space: nowrap; }
        .upd-banner__close { border: none; background: transparent; color: #fff; opacity: .85; cursor: pointer; padding: 4px; }
      `}</style>
      <div className="upd-banner" role="status">
        <div className="upd-banner__text">
          Доступна новая версия <strong>{info.latest}</strong> (у вас {info.current})
        </div>
        <button type="button" className="upd-banner__btn" disabled={locked || applying} onClick={installUpdate} title={locked ? 'Отключитесь перед обновлением' : undefined}>
          <IconDownload size={15} />
          {applying ? '…' : 'Установить'}
        </button>
        <button type="button" className="upd-banner__close" aria-label="Скрыть" onClick={dismiss}>
          <IconX size={16} />
        </button>
      </div>
    </>
  );
}
