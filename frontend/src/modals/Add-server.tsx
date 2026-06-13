import { useState } from 'react';
import { IconCircleHalf2 } from '@tabler/icons-react';
import type { Server } from '../lib/types';
import { SaveProfile } from '../../wailsjs/go/backend/App';
import { parseWdttUrl, resolveWdttImport, isImportableInput, isHttpUrl } from '../lib/utils/wdttLink';
import { handleControlledPaste } from '../lib/utils/inputPaste';
import { toastStore } from '../lib/stores/toastStore';

interface Props {
  onClose: () => void;
  onAdd: (server: Omit<Server, 'id'>) => void;
}

const lockedStyle = { opacity: 0.65, cursor: 'not-allowed' as const };

export default function AddServer({ onClose, onAdd }: Props) {
  const [link, setLink] = useState('');
  const [linkLocked, setLinkLocked] = useState(false);
  const [name, setName] = useState('');
  const [vpnName, setVpnName] = useState('');
  const [ip, setIp] = useState('');
  const [port, setPort] = useState('56000');
  const [password, setPassword] = useState('');
  const [subUrl, setSubUrl] = useState('');

  const applyParsed = (parsed: NonNullable<Awaited<ReturnType<typeof resolveWdttImport>>>) => {
    setIp(parsed.ip);
    setPort(parsed.dtlsPort);
    setPassword(parsed.password);
    setName(parsed.name !== 'Server' ? parsed.name : parsed.ip);
    if (parsed.vpnName) setVpnName(parsed.vpnName);
    if (parsed.subUrl) setSubUrl(parsed.subUrl);
    setLinkLocked(true);
  };

  const applyLink = (raw: string) => {
    setLink(raw);
    const trimmed = raw.trim();
    if (!trimmed) {
      setLinkLocked(false);
      return;
    }
    void (async () => {
      if (isHttpUrl(trimmed)) setSubUrl(trimmed.split('?')[0]);
      const parsed = await resolveWdttImport(trimmed);
      if (parsed) {
        applyParsed(parsed);
        return;
      }
      const legacy = parseWdttUrl(trimmed);
      if (legacy) {
        setIp(legacy.ip);
        setPort(legacy.dtlsPort);
        setPassword(legacy.password);
        if (legacy.name !== 'Server') setName(legacy.name);
        if (legacy.vpnName) setVpnName(legacy.vpnName);
        if (legacy.subUrl) setSubUrl(legacy.subUrl);
        setLinkLocked(true);
        return;
      }
      setLinkLocked(false);
      if (isImportableInput(trimmed)) {
        toastStore.show('Подписка не загрузилась — заполните поля вручную или проверьте URL', 4000);
      }
    })();
  };

  const canAdd = Boolean(name.trim() && ip.trim() && password.trim())
    || isImportableInput(link)
    || isHttpUrl(subUrl);

  const handleAdd = async () => {
    let finalName = name.trim();
    let finalIp = ip.trim();
    let finalPort = port.trim() || '56000';
    let finalPassword = password;
    let finalSubUrl = subUrl.trim().split('?')[0];
    let fromLink = linkLocked;

    const parsed = await resolveWdttImport(link.trim());
    const legacy = parsed ?? parseWdttUrl(link.trim());
    if (parsed) {
      finalName = finalName || parsed.name;
      finalIp = finalIp || parsed.ip;
      finalPort = finalPort || parsed.dtlsPort;
      finalPassword = finalPassword || parsed.password;
      finalSubUrl = finalSubUrl || parsed.subUrl || '';
      fromLink = true;
    } else if (legacy) {
      finalName = finalName || legacy.name;
      finalIp = finalIp || legacy.ip;
      finalPort = finalPort || legacy.dtlsPort;
      finalPassword = finalPassword || legacy.password;
      finalSubUrl = finalSubUrl || legacy.subUrl || '';
      fromLink = true;
    } else if (isHttpUrl(link.trim())) {
      finalSubUrl = finalSubUrl || link.trim().split('?')[0];
    }

    if (!finalName || !finalIp || !finalPassword) {
      toastStore.show('Нужны название, IP и пароль (или рабочая ссылка sub/wdtt)', 4000);
      return;
    }

    const host = `${finalIp}:${finalPort}`;
    const hashes = legacy?.hashes ?? parsed?.hashes ?? [];

    await SaveProfile(finalName, {
      peer: host,
      password: finalPassword,
      hashes,
      turn: '', port: '', device_id: '', listen: '',
    });

    const h4: [string,string,string,string] = [hashes[0]??'', hashes[1]??'', hashes[2]??'', hashes[3]??''];
    onAdd({
      name: finalName,
      vpnName: vpnName.trim() || parsed?.vpnName || legacy?.vpnName || undefined,
      host,
      password: finalPassword,
      hashes: h4,
      subUrl: finalSubUrl || undefined,
      linkManaged: fromLink || undefined,
    });
    onClose();
  };

  return (
    <>
      <style>{`
        .as-overlay { position: fixed; inset: 0; background: var(--overlay-bg); backdrop-filter: blur(4px); display: flex; align-items: center; justify-content: center; z-index: 100; animation: overlay-in 0.3s ease-out; }
        .as-modal { background: var(--surface); border-radius: 14px; padding: 20px; width: 380px; max-width: 95vw; box-shadow: var(--shadow); border: 1px solid var(--border); max-height: 90vh; overflow-y: auto; animation: modal-in 0.3s ease-out; }
        .as-header { display: flex; align-items: center; gap: 10px; margin-bottom: 18px; color: var(--text); }
        .as-title { font-size: 16px; font-weight: 600; flex: 1; color: var(--text); }
        .as-close { background: none; border: none; cursor: pointer; font-size: 18px; color: var(--text); line-height: 1; padding: 0; }
        .as-input { width: 100%; padding: 11px 14px; border: 1.5px solid var(--input-border); border-radius: 10px; font-size: 14px; font-family: 'Geist', sans-serif; outline: none; margin-bottom: 10px; box-sizing: border-box; color: var(--text); background: var(--input-bg); }
        .as-input::placeholder { color: var(--text-4); }
        .as-input--locked { opacity: 0.65; cursor: not-allowed; }
        .as-divider { display: flex; align-items: center; gap: 8px; margin: 4px 0 12px; color: var(--text-4); font-size: 12px; }
        .as-divider::before, .as-divider::after { content: ''; flex: 1; height: 1px; background: var(--border); }
        .as-hint { font-size: 11px; color: var(--text-3); margin: -4px 0 10px; line-height: 1.35; }
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
            placeholder="Ссылка wdtt:// или любой https://..."
            value={link}
            onChange={e => applyLink(e.target.value)}
            onPaste={e => void handleControlledPaste(e, link, applyLink)}
          />

          {linkLocked ? (
            <>
              <div className="as-divider">данные из ссылки</div>
              <p className="as-hint">Поля заполнены автоматически и не редактируются. Очистите ссылку выше, чтобы ввести вручную.</p>
            </>
          ) : (
            <div className="as-divider">или вручную</div>
          )}

          <input
            className={`as-input${linkLocked ? ' as-input--locked' : ''}`}
            placeholder="Комментарий (name)"
            value={name}
            readOnly={linkLocked}
            style={linkLocked ? lockedStyle : undefined}
            onChange={e => { if (!linkLocked) setName(e.target.value); }}
            onPaste={e => { if (!linkLocked) void handleControlledPaste(e, name, setName); }}
          />
          <input
            className={`as-input${linkLocked ? ' as-input--locked' : ''}`}
            placeholder="Название VPN (vpn)"
            value={vpnName}
            readOnly={linkLocked}
            style={linkLocked ? lockedStyle : undefined}
            onChange={e => { if (!linkLocked) setVpnName(e.target.value); }}
            onPaste={e => { if (!linkLocked) void handleControlledPaste(e, vpnName, setVpnName); }}
          />
          <div style={{ display: 'flex', gap: 8 }}>
            <input
              className={`as-input${linkLocked ? ' as-input--locked' : ''}`}
              style={{ flex: 1, ...(linkLocked ? lockedStyle : {}) }}
              placeholder="IP сервера"
              value={ip}
              readOnly={linkLocked}
              onChange={e => { if (!linkLocked) setIp(e.target.value); }}
              onPaste={e => { if (!linkLocked) void handleControlledPaste(e, ip, setIp); }}
            />
            <input
              className={`as-input${linkLocked ? ' as-input--locked' : ''}`}
              style={{ width: 100, ...(linkLocked ? lockedStyle : {}) }}
              placeholder="Порт"
              value={port}
              readOnly={linkLocked}
              onChange={e => { if (!linkLocked) setPort(e.target.value); }}
              onPaste={e => { if (!linkLocked) void handleControlledPaste(e, port, setPort); }}
            />
          </div>
          <input
            className="as-input"
            placeholder="Пароль туннеля"
            type="password"
            value={password}
            readOnly={linkLocked}
            style={linkLocked ? lockedStyle : undefined}
            onChange={e => { if (!linkLocked) setPassword(e.target.value); }}
            onPaste={e => { if (!linkLocked) void handleControlledPaste(e, password, setPassword); }}
          />
          <input
            className={`as-input${linkLocked ? ' as-input--locked' : ''}`}
            placeholder="URL подписки (любой https://...) — для метрик"
            value={subUrl}
            readOnly={linkLocked}
            style={linkLocked ? lockedStyle : undefined}
            onChange={e => { if (!linkLocked) setSubUrl(e.target.value); }}
            onPaste={e => { if (!linkLocked) void handleControlledPaste(e, subUrl, setSubUrl); }}
          />
          <button className="as-btn" onClick={handleAdd} disabled={!canAdd}>Добавить сервер</button>
        </div>
      </div>
    </>
  );
}
