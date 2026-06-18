import { useState } from 'react';
import { IconCircleHalf2 } from '@tabler/icons-react';
import type { Server } from '../lib/types';
import { SaveProfile } from '../../wailsjs/go/backend/App';
import { resolveWdttImport, isPanelSubUrl } from '../lib/utils/wdttLink';
import { handleControlledPaste } from '../lib/utils/inputPaste';
import { toastStore } from '../lib/stores/toastStore';

interface Props {
  onClose: () => void;
  onAdd: (server: Omit<Server, 'id'>) => void;
}

export default function AddServer({ onClose, onAdd }: Props) {
  const [subUrl, setSubUrl] = useState('');
  const [loading, setLoading] = useState(false);
  const [parsed, setParsed] = useState<Awaited<ReturnType<typeof resolveWdttImport>> | null>(null);

  const applySubUrl = (raw: string) => {
    setSubUrl(raw);
    setParsed(null);
    const trimmed = raw.trim().split('?')[0];
    if (!trimmed) return;
    if (!isPanelSubUrl(trimmed) && !trimmed.startsWith('wdtt://')) {
      toastStore.show('Вставьте ссылку подписки или wdtt:// с полем sub из панели WDTT', 4500);
      return;
    }
    void (async () => {
      setLoading(true);
      try {
        const link = await resolveWdttImport(trimmed);
        if (!link) {
          toastStore.show('Не удалось загрузить подписку — проверьте ссылку и доступ к панели', 4500);
          return;
        }
        setParsed(link);
      } finally {
        setLoading(false);
      }
    })();
  };

  const canAdd = Boolean(parsed && !loading);

  const handleAdd = async () => {
    if (!parsed?.subUrl) {
      toastStore.show('Сначала загрузите подписку из панели', 4000);
      return;
    }
    const host = `${parsed.ip}:${parsed.dtlsPort}`;
    const name = parsed.name !== 'Server' ? parsed.name : parsed.ip;
    const hashes = parsed.hashes ?? [];
    const h4: [string, string, string, string] = [hashes[0] ?? '', hashes[1] ?? '', hashes[2] ?? '', hashes[3] ?? ''];

    await SaveProfile(name, {
      peer: host,
      password: parsed.password,
      hashes: [],
      turn: '', port: '', device_id: parsed.deviceId ?? '', listen: '',
    });

    onAdd({
      name,
      vpnName: parsed.vpnName,
      host,
      password: parsed.password,
      deviceId: parsed.deviceId,
      hashes: h4,
      subUrl: parsed.subUrl,
      linkManaged: true,
    });
    onClose();
  };

  return (
    <>
      <style>{`
        .as-overlay { position: fixed; inset: 0; background: var(--overlay-bg); backdrop-filter: blur(4px); display: flex; align-items: flex-start; justify-content: center; padding: 16px 0; z-index: 100; animation: overlay-in 0.3s ease-out; overflow: hidden; }
        .as-modal { background: var(--surface); border-radius: 14px; padding: 16px 18px; width: 440px; max-width: calc(100vw - 24px); box-shadow: var(--shadow); border: 1px solid var(--border); overflow: visible; flex-shrink: 0; animation: modal-in 0.3s ease-out; }
        .as-header { display: flex; align-items: center; gap: 10px; margin-bottom: 12px; color: var(--text); }
        .as-title { font-size: 16px; font-weight: 600; flex: 1; color: var(--text); }
        .as-close { background: none; border: none; cursor: pointer; font-size: 18px; color: var(--text); line-height: 1; padding: 0; }
        .as-input { width: 100%; padding: 11px 14px; border: 1.5px solid var(--input-border); border-radius: 10px; font-size: 14px; font-family: 'Geist', sans-serif; outline: none; margin-bottom: 10px; box-sizing: border-box; color: var(--text); background: var(--input-bg); }
        .as-input::placeholder { color: var(--text-4); }
        .as-hint { font-size: 11px; color: var(--text-3); margin: -4px 0 12px; line-height: 1.4; }
        .as-preview { background: var(--input-bg); border: 1px solid var(--border); border-radius: 10px; padding: 12px 14px; margin-bottom: 12px; font-size: 13px; color: var(--text-2); line-height: 1.5; }
        .as-preview strong { color: var(--text); }
        .as-btn { width: 100%; padding: 13px; border: none; border-radius: 10px; background: var(--accent); color: var(--accent-fg); font-size: 14px; font-family: 'Geist', sans-serif; font-weight: 600; cursor: pointer; margin-top: 4px; }
        .as-btn:disabled { opacity: 0.4; cursor: not-allowed; }
      `}</style>
      <div className="as-overlay" onClick={onClose}>
        <div className="as-modal" onClick={e => e.stopPropagation()}>
          <div className="as-header">
            <IconCircleHalf2 stroke={2} size={22} />
            <span className="as-title">Добавление сервера</span>
            <button className="as-close" onClick={onClose}>✕</button>
          </div>

          <input
            className="as-input"
            placeholder="https://…/subs/… или wdtt://… (с полем sub)"
            value={subUrl}
            onChange={e => applySubUrl(e.target.value)}
            onPaste={e => void handleControlledPaste(e, subUrl, applySubUrl)}
          />
          <p className="as-hint">
            Скопируйте «Подписка» или «Ссылку» из панели WDTT. Если вставляете wdtt:// — внутри должен быть URL подписки (поле sub).
          </p>

          {loading && <p className="as-hint">Загрузка подписки…</p>}

          {parsed && (
            <div className="as-preview">
              <div><strong>{parsed.vpnName || parsed.name}</strong></div>
              <div>{parsed.ip}:{parsed.dtlsPort}</div>
              <div style={{ opacity: 0.75, fontSize: 12, marginTop: 4 }}>{parsed.subUrl}</div>
            </div>
          )}

          <button className="as-btn" onClick={handleAdd} disabled={!canAdd}>
            {loading ? 'Загрузка…' : 'Добавить сервер'}
          </button>
        </div>
      </div>
    </>
  );
}
