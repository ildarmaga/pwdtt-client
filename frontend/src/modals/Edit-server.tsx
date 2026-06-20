import { useState } from 'react';
import type React from 'react';
import { IconCircleHalf2, IconInfoCircle, IconHash } from '@tabler/icons-react';
import type { Server } from '../lib/types';
import { isLinkManagedServer } from '../lib/types';
import { settingsStore } from '../lib/store';
import Hash from './Hash';
import { handleControlledPaste } from '../lib/utils/inputPaste';
import { isPanelSubUrl } from '../lib/utils/wdttLink';
import { toastStore } from '../lib/stores/toastStore';
import { DeleteProfile } from '../../wailsjs/go/backend/App';
import { saveServerProfile } from '../lib/utils/profileSync';

interface Props {
  server: Server;
  onClose: () => void;
  onSave: (server: Server) => void;
  onDelete: (id: string) => void;
}

export default function EditServer({ server, onClose, onSave, onDelete }: Props) {
  const [name, setName] = useState(server.name);
  const [vpnName, setVpnName] = useState(server.vpnName ?? '');
  const lastColon = server.host.lastIndexOf(':');
  const ip = lastColon !== -1 ? server.host.slice(0, lastColon) : server.host;
  const port0 = lastColon !== -1 ? server.host.slice(lastColon + 1) : '56000';
  const [serverIp, setServerIp] = useState(ip);
  const [serverPort, setServerPort] = useState(port0);
  const [password, setPassword] = useState(server.password);
  const [subUrl, setSubUrl] = useState(server.subUrl ?? '');
  const [hashes, setHashes] = useState<[string,string,string,string]>(
    server.hashes ?? ['', '', '', '']
  );
  const [hashOpen, setHashOpen] = useState(false);

  const globalOn = settingsStore.get().useGlobalHashes;
  const locked = isLinkManagedServer(server);
  const filledHashes = hashes.filter(h => h.trim()).length;
  const powerMax = Math.max(9, filledHashes * 27);
  const [power, setPower] = useState<number>(server.power ?? Math.max(9, filledHashes * 9));

  const handleHashSave = (h: [string, string, string, string]) => {
    setHashes(h);
    const filled = h.filter(x => x.trim()).length;
    const newMax = Math.max(9, filled * 27);
    // если текущая мощность больше нового макс или не была задана вручную — подстрой
    setPower(p => Math.min(p, newMax) || Math.max(9, filled * 9));
  };

  const handleSave = async () => {
    if (!name.trim() || !serverIp.trim()) return;
    const trimmedSub = subUrl.trim().split('?')[0];
    if (trimmedSub && !isPanelSubUrl(trimmedSub)) {
      toastStore.show('URL подписки должен быть ссылкой WDTT-панели', 4000);
      return;
    }
    const updated: Server = {
      ...server,
      name: name.trim(),
      vpnName: vpnName.trim() || undefined,
      host: `${serverIp.trim()}:${serverPort.trim() || '56000'}`,
      password,
      subUrl: trimmedSub || undefined,
      hashes,
      power,
    };
    // Профиль на диске ключуется по неизменному id сервера, поэтому при
    // переименовании старый файл удалять не нужно — id остаётся прежним.
    await saveServerProfile(updated);
    onSave(updated);
    onClose();
  };

  const handleDelete = async () => {
    await DeleteProfile(server.id).catch(() => {});
    onDelete(server.id);
    onClose();
  };

  return (
    <>
      <style>{`
        .es-overlay { position: fixed; inset: 0; background: var(--overlay-bg); backdrop-filter: blur(4px); display: flex; align-items: flex-start; justify-content: center; padding: 16px 0; z-index: 100; animation: overlay-in 0.3s ease-out; overflow: hidden; }
        .es-modal { background: var(--surface); border-radius: 14px; padding: 16px 18px; width: 440px; max-width: calc(100vw - 24px); box-shadow: var(--shadow); border: 1px solid var(--border); overflow: visible; flex-shrink: 0; animation: modal-in 0.3s ease-out; }
        .es-header { display: flex; align-items: center; gap: 10px; margin-bottom: 12px; color: var(--text); }
        .es-title { font-size: 16px; font-weight: 600; flex: 1; color: var(--text); }
        .es-close { background: none; border: none; cursor: pointer; font-size: 18px; color: var(--text); line-height: 1; padding: 0; }
        .es-input { width: 100%; padding: 9px 12px; border: 1.5px solid var(--input-border); border-radius: 10px; font-size: 14px; font-family: 'Geist', sans-serif; outline: none; margin-bottom: 8px; box-sizing: border-box; color: var(--text); background: var(--input-bg); }
        .es-input::placeholder { color: var(--text-4); }
        .es-hash-btn { width: 100%; margin-top: 2px; margin-bottom: 8px; padding: 11px; border: 1.5px solid var(--border); border-radius: 10px; background: var(--surface); color: var(--text); font-size: 14px; font-family: 'Geist', sans-serif; font-weight: 600; cursor: pointer; display: flex; align-items: center; justify-content: center; gap: 8px; }
        .es-btn-row { display: flex; gap: 10px; margin-top: 4px; }
        .es-btn { flex: 1; padding: 13px; border: none; border-radius: 10px; font-size: 14px; font-family: 'Geist', sans-serif; font-weight: 600; cursor: pointer; }
        .es-btn--save { background: var(--accent); color: var(--accent-fg); }
        .es-btn--save:disabled { opacity: 0.4; cursor: not-allowed; }
        .es-btn--delete { background: #cc0000; color: #fff; }
        .es-input--locked { opacity: 0.65; cursor: not-allowed; }
        .es-hint-lock { font-size: 11px; color: var(--text-3); margin: -4px 0 10px; line-height: 1.35; }
        .es-slider-wrap { padding: 2px 0 8px; border-bottom: 1px solid var(--border-2); margin-bottom: 8px; }
        .es-slider-label { display: flex; justify-content: space-between; font-size: 14px; color: var(--text); margin-bottom: 8px; }
        .es-slider { width: 100%; -webkit-appearance: none; appearance: none; height: 4px; border-radius: 2px; outline: none; cursor: pointer; background: linear-gradient(to right, var(--accent) calc(var(--v) * 1%), var(--border) calc(var(--v) * 1%)); }
        .es-slider::-webkit-slider-thumb { -webkit-appearance: none; width: 18px; height: 18px; border-radius: 50%; background: var(--surface); border: 2px solid var(--accent); cursor: pointer; }
        .es-slider--disabled { opacity: 0.4; pointer-events: none; }
      `}</style>
      <div className="es-overlay" onClick={onClose}>
        <div className="es-modal" onClick={e => e.stopPropagation()}>
          <div className="es-header">
            <IconCircleHalf2 size={22} />
            <span className="es-title">Редактирование сервера</span>
            <button className="es-close" onClick={onClose}>✕</button>
          </div>

          {locked && (
            <p className="es-hint-lock">
              Сервер добавлен по ссылке или подписке — название, адрес и URL подписки нельзя изменить.
            </p>
          )}

          <input
            className={`es-input${locked ? ' es-input--locked' : ''}`}
            placeholder="Комментарий (name)"
            value={name}
            readOnly={locked}
            onChange={e => { if (!locked) setName(e.target.value); }}
            onPaste={e => { if (!locked) void handleControlledPaste(e, name, setName); }}
          />
          <input
            className={`es-input${locked ? ' es-input--locked' : ''}`}
            placeholder="Название VPN (vpn)"
            value={vpnName}
            readOnly={locked}
            onChange={e => { if (!locked) setVpnName(e.target.value); }}
            onPaste={e => { if (!locked) void handleControlledPaste(e, vpnName, setVpnName); }}
          />
          <div style={{ display: 'flex', gap: 8 }}>
            <input
              className={`es-input${locked ? ' es-input--locked' : ''}`}
              style={{ flex: 1 }}
              placeholder="IP сервера"
              value={serverIp}
              readOnly={locked}
              onChange={e => { if (!locked) setServerIp(e.target.value); }}
              onPaste={e => { if (!locked) void handleControlledPaste(e, serverIp, setServerIp); }}
            />
            <input
              className={`es-input${locked ? ' es-input--locked' : ''}`}
              style={{ width: 100 }}
              placeholder="Порт"
              value={serverPort}
              readOnly={locked}
              onChange={e => { if (!locked) setServerPort(e.target.value); }}
              onPaste={e => { if (!locked) void handleControlledPaste(e, serverPort, setServerPort); }}
            />
          </div>
          <input className="es-input" placeholder="Пароль туннеля" type="password" value={password} onChange={e => setPassword(e.target.value)} onPaste={e => void handleControlledPaste(e, password, setPassword)} />
          <input
            className={`es-input${locked ? ' es-input--locked' : ''}`}
            placeholder="URL подписки (https://.../sub/...) — для метрик"
            value={subUrl}
            readOnly={locked}
            onChange={e => { if (!locked) setSubUrl(e.target.value); }}
            onPaste={e => { if (!locked) void handleControlledPaste(e, subUrl, setSubUrl); }}
          />

          <div className={`es-slider-wrap${globalOn ? ' es-slider--disabled' : ''}`}>
            <div className="es-slider-label">
              <span>Мощность</span>
              <span>{globalOn ? '—' : (filledHashes === 0 ? 'нет хешей' : power)}</span>
            </div>
            <input
              type="range" min={9} max={powerMax} step={9}
              value={Math.min(power, powerMax)}
              className="es-slider"
              disabled={globalOn || filledHashes === 0}
              style={{ '--v': filledHashes > 0 ? Math.round((Math.min(power, powerMax) - 9) / Math.max(powerMax - 9, 1) * 100) : 0 } as React.CSSProperties}
              onChange={e => setPower(+e.target.value)}
            />
          </div>

          <button
            className="es-hash-btn"
            style={globalOn ? { opacity: 0.35, pointerEvents: 'none' } : undefined}
            onClick={() => setHashOpen(true)}
            title={globalOn ? 'Включены глобальные хеши — профильные не используются' : undefined}
          >
            <IconHash stroke={2} size={16} />
            Хеши профиля ({filledHashes}/4)
            {globalOn && <IconInfoCircle size={14} style={{ marginLeft: 4, color: 'var(--text-3)' }} />}
          </button>
          {globalOn && (
            <div className="es-hint" style={{ display: 'flex', alignItems: 'center', gap: 5, fontSize: 11, color: 'var(--text-3)', marginBottom: 8 }}>
              <IconInfoCircle size={11} />
              Отключите «Глобальные хеши» в Настройках, чтобы использовать хеши профиля
            </div>
          )}

          <div className="es-btn-row">
            <button className="es-btn es-btn--save" onClick={handleSave} disabled={!name.trim() || !serverIp.trim()}>Сохранить</button>
            <button className="es-btn es-btn--delete" onClick={handleDelete}>Удалить</button>
          </div>
        </div>
      </div>
      {hashOpen && (
        <Hash
          hashes={hashes}
          onClose={() => setHashOpen(false)}
          onSave={handleHashSave}
        />
      )}
    </>
  );
}
