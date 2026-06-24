import { useState, useEffect } from 'react';
import { IconSettings2, IconHash, IconChevronDown, IconCopy, IconCheck } from '@tabler/icons-react';
import Hash from './Hash';
import { settingsStore, serverStore } from '../lib/store';
import { selectedServerStore } from '../lib/stores/selectedServerStore';
import { tunnelStore } from '../lib/stores/tunnelStore';
import type { AppSettings } from '../lib/types';
import { METRICS_REFRESH_OPTIONS } from '../lib/types';
import { SetTrayEnabled, SetAutoStart, GetAutoStart, GetProfile, GetVKCookiesStatus, SaveVKCookies, ClearVKCookies } from '../../wailsjs/go/backend/App';
import type { backend } from '../../wailsjs/go/models';

interface Props {
  onClose: () => void;
}

export default function Settings({ onClose }: Props) {
  const [settings, setSettings] = useState<AppSettings>(() => settingsStore.get());
  const [hashOpen, setHashOpen] = useState(false);
  const [mtuRaw, setMtuRaw] = useState(String(settingsStore.get().mtu ?? 1280));
  const mtuValid = (() => { const n = Number(mtuRaw); return Number.isInteger(n) && n >= 576 && n <= 1500; })();
  const [tunnelState, setTunnelState] = useState(() => tunnelStore.get());
  useEffect(() => tunnelStore.subscribe(setTunnelState), []);
  const locked = tunnelState === 'connected' || tunnelState === 'connecting';
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const [advancedConfirm, setAdvancedConfirm] = useState(false);
  const [deviceId, setDeviceId] = useState('');
  const [idCopied, setIdCopied] = useState(false);
  const [vkCookies, setVkCookies] = useState('');
  const [vkStatus, setVkStatus] = useState<backend.VKCookiesStatus | null>(null);
  const [vkSaveMsg, setVkSaveMsg] = useState('');

  const refreshVkStatus = () => {
    GetVKCookiesStatus().then(setVkStatus).catch(() => setVkStatus(null));
  };

  useEffect(() => { refreshVkStatus(); }, []);

  // Sync autoStart from backend on open
  useEffect(() => {
    GetAutoStart().then(v => {
      if (v !== settings.autoStart) update('autoStart', v);
    });
  }, []);

  // ID устройства привязан к профилю выбранного сервера — показываем его.
  useEffect(() => {
    const all = serverStore.getAll();
    const srv = all.find(s => s.id === selectedServerStore.getId()) ?? all[0];
    if (!srv) return;
    GetProfile(srv.id)
      .then(p => { if (p?.device_id) setDeviceId(p.device_id); })
      .catch(() => {});
  }, []);

  const copyDeviceId = () => {
    if (!deviceId) return;
    navigator.clipboard?.writeText(deviceId).catch(() => {});
    setIdCopied(true);
    setTimeout(() => setIdCopied(false), 1500);
  };

  const update = <K extends keyof AppSettings>(key: K, value: AppSettings[K]) => {
    setSettings(s => ({ ...s, [key]: value }));
  };

  const handleClose = () => {
    const n = Number(mtuRaw);
    const mtu = mtuValid ? n : settings.mtu;
    settingsStore.save({ ...settings, mtu });
    onClose();
  };

  const filledHashes = settings.hashes.filter(h => h.trim()).length;
  const powerMax = Math.max(9, filledHashes * 27);

  return (
    <>
      <style>{`
        .st-overlay { position: fixed; inset: 0; background: var(--overlay-bg); backdrop-filter: blur(4px); display: flex; align-items: flex-start; justify-content: center; padding: 16px 0; z-index: 100; animation: overlay-in 0.3s ease-out; overflow: hidden; }
        .st-modal { background: var(--surface); border-radius: 14px; padding: 16px 18px; width: 440px; max-width: calc(100vw - 24px); box-shadow: var(--shadow); animation: modal-in 0.3s ease-out; border: 1px solid var(--border); overflow: visible; flex-shrink: 0; }
        .st-header { display: flex; align-items: center; gap: 10px; margin-bottom: 12px; color: var(--text); }
        .st-title { font-size: 16px; font-weight: 600; flex: 1; color: var(--text); }
        .st-close { background: none; border: none; cursor: pointer; font-size: 18px; color: var(--text); line-height: 1; padding: 0; }
        .st-row { display: flex; align-items: center; justify-content: space-between; padding: 8px 0; border-bottom: 1px solid var(--border-2); font-size: 14px; color: var(--text); }
        .st-row--stack { flex-direction: column; align-items: stretch; gap: 6px; }
        .st-row-head { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
        .st-row-hint { font-size: 11px; color: var(--text-3); line-height: 1.4; }
        .st-row:last-of-type { border-bottom: none; }
        .st-toggle { width: 48px; height: 26px; border-radius: 50px; border: none; cursor: pointer; position: relative; transition: background 0.2s; flex-shrink: 0; }
        .st-toggle--on { background: var(--accent); }
        .st-toggle--off { background: var(--seg-bg); }
        .st-toggle::after { content: ''; position: absolute; width: 18px; height: 18px; border-radius: 50%; top: 4px; transition: left 0.2s; }
        .st-toggle--on::after { background: var(--accent-fg); left: 26px; }
        .st-toggle--off::after { background: var(--text-3); left: 4px; }
        .st-seg { display: flex; background: var(--seg-bg); border-radius: 8px; padding: 2px; gap: 2px; }
        .st-seg-btn { padding: 5px 13px; border: none; border-radius: 6px; font-size: 12px; font-weight: 600; cursor: pointer; transition: background 0.15s, color 0.15s; background: transparent; color: var(--seg-text); }
        .st-seg-btn--active { background: var(--accent); color: var(--accent-fg); }
        .st-slider-wrap { padding: 2px 0 8px; border-bottom: 1px solid var(--border-2); }
        .st-slider-label { display: flex; justify-content: space-between; font-size: 14px; color: var(--text); margin-bottom: 8px; }
        .st-slider { width: 100%; -webkit-appearance: none; appearance: none; height: 4px; border-radius: 2px; outline: none; cursor: pointer; background: linear-gradient(to right, var(--accent) calc(var(--v) * 1%), var(--border) calc(var(--v) * 1%)); }
        .st-slider::-webkit-slider-thumb { -webkit-appearance: none; width: 18px; height: 18px; border-radius: 50%; background: var(--surface); border: 2px solid var(--accent); cursor: pointer; }
        .st-num-input { width: 80px; padding: 5px 10px; border: 1.5px solid var(--border); border-radius: 8px; font-size: 14px; font-family: 'Geist', sans-serif; text-align: right; outline: none; background: var(--input-bg); color: var(--text); transition: border-color 0.15s; }
        .st-num-input:focus { border-color: var(--accent); }
        .st-num-input--error { border-color: #ef4444; }
        .st-select { padding: 5px 8px; border: 1.5px solid var(--border); border-radius: 8px; font-size: 13px; font-family: 'Geist', sans-serif; background: var(--input-bg); color: var(--text); outline: none; cursor: pointer; max-width: 130px; }
        .st-select:focus { border-color: var(--accent); }
        .st-hash-btn { width: 100%; margin-top: 10px; padding: 11px; border: 1.5px solid var(--border); border-radius: 10px; background: var(--surface); color: var(--text); font-size: 14px; font-family: 'Geist', sans-serif; font-weight: 600; cursor: pointer; display: flex; align-items: center; justify-content: center; gap: 8px; }
        .st-devid-block { padding: 8px 0; border-bottom: 1px solid var(--border-2); }
        .st-devid-label { font-size: 14px; color: var(--text); margin-bottom: 6px; }
        .st-devid { display: flex; align-items: flex-start; gap: 8px; width: 100%; box-sizing: border-box; background: var(--seg-bg); border: none; border-radius: 8px; padding: 8px 10px; cursor: pointer; color: var(--text-2); font-family: 'Geist Mono', ui-monospace, monospace; font-size: 11px; text-align: left; }
        .st-devid:hover { color: var(--text); }
        .st-devid-val { flex: 1; min-width: 0; line-height: 1.4; word-break: break-all; white-space: normal; overflow: visible; direction: ltr; }
        .st-devid svg { flex-shrink: 0; margin-top: 1px; }
        .st-locked { opacity: 0.4; pointer-events: none; }
        .st-lock-hint { font-size: 11px; color: var(--text-3); margin-bottom: 4px; text-align: center; }
        .st-adv-toggle { width: 100%; display: flex; align-items: center; justify-content: space-between; background: none; border: none; border-top: 1px solid var(--border-2); padding: 8px 0 0; cursor: pointer; font-size: 13px; font-weight: 600; color: var(--text-3); font-family: 'Geist', sans-serif; margin-top: 2px; }
        .st-adv-toggle svg { transition: transform 0.2s; }
        .st-adv-toggle--open svg { transform: rotate(180deg); }
        .st-adv-body { overflow: hidden; transition: max-height 0.25s ease, opacity 0.2s; }
        .st-adv-body--open { max-height: 300px; opacity: 1; }
        .st-adv-body--closed { max-height: 0; opacity: 0; pointer-events: none; }
        .st-confirm-overlay { position: fixed; inset: 0; background: var(--overlay-bg); backdrop-filter: blur(4px); display: flex; align-items: center; justify-content: center; z-index: 200; }
        .st-confirm { background: var(--surface); border-radius: 14px; padding: 22px 20px 18px; width: 320px; max-width: 92vw; box-shadow: var(--shadow); border: 1px solid var(--border); }
        .st-confirm-title { font-size: 15px; font-weight: 700; color: var(--text); margin-bottom: 8px; }
        .st-confirm-text { font-size: 13px; color: var(--text-2); line-height: 1.5; margin-bottom: 18px; }
        .st-confirm-actions { display: flex; gap: 8px; justify-content: flex-end; }
        .st-confirm-btn { padding: 8px 18px; border-radius: 8px; border: none; font-size: 13px; font-family: 'Geist', sans-serif; font-weight: 600; cursor: pointer; }
        .st-confirm-btn--cancel { background: var(--seg-bg); color: var(--text); }
        .st-confirm-btn--ok { background: var(--accent); color: var(--accent-fg); }
        .st-vk-block { margin-top: 10px; padding-top: 10px; border-top: 1px solid var(--border-2); }
        .st-vk-hint { font-size: 11px; color: var(--text-3); line-height: 1.45; margin-bottom: 8px; }
        .st-vk-status { font-size: 12px; margin-bottom: 8px; color: var(--text-2); }
        .st-vk-status--ok { color: #22c55e; }
        .st-vk-status--bad { color: #ef4444; }
        .st-vk-textarea { width: 100%; min-height: 72px; resize: vertical; padding: 8px 10px; border: 1.5px solid var(--border); border-radius: 8px; font-size: 11px; font-family: 'Geist Mono', ui-monospace, monospace; background: var(--input-bg); color: var(--text); box-sizing: border-box; }
        .st-vk-actions { display: flex; gap: 8px; margin-top: 8px; }
        .st-vk-btn { flex: 1; padding: 8px 10px; border-radius: 8px; border: 1.5px solid var(--border); background: var(--surface); color: var(--text); font-size: 12px; font-weight: 600; cursor: pointer; }
        .st-vk-btn--primary { background: var(--accent); color: var(--accent-fg); border-color: var(--accent); }
        .st-vk-msg { font-size: 11px; color: var(--text-3); margin-top: 6px; min-height: 16px; }
      `}</style>
      <div className="st-overlay" onClick={handleClose}>
        <div className="st-modal" onClick={e => e.stopPropagation()}>
          <div className="st-header">
            <IconSettings2 stroke={2} size={20} />
            <span className="st-title">Настройки</span>
            <button className="st-close" onClick={handleClose}>✕</button>
          </div>

          {locked && <div className="st-lock-hint">Недоступно во время подключения</div>}

          {settings.useGlobalHashes ? (
            <div className={`st-slider-wrap${locked ? ' st-locked' : ''}`}>
              <div className="st-slider-label"><span>Мощность</span><span>{settings.power}</span></div>
              <input
                type="range" min={9} max={powerMax} step={9} value={Math.min(settings.power, powerMax)}
                className="st-slider"
                style={{ '--v': Math.round((Math.min(settings.power, powerMax) - 9) / Math.max(powerMax - 9, 1) * 100) } as React.CSSProperties}
                onChange={e => update('power', +e.target.value)}
              />
            </div>
          ) : (
            <div className="st-slider-wrap" style={{ opacity: 0.5 }}>
              <div className="st-slider-label"><span>Мощность</span><span>профиль</span></div>
              <div style={{ fontSize: 12, color: 'var(--text-3)' }}>Настраивается в редакторе профиля</div>
            </div>
          )}

          <div className="st-row">
            <span>Трей</span>
            <button className={`st-toggle st-toggle--${settings.tray ? 'on' : 'off'}`} onClick={() => {
              const next = !settings.tray;
              update('tray', next);
              SetTrayEnabled(next);
            }} />
          </div>

          <div className="st-row">
            <span>Запускать при старте</span>
            <button className={`st-toggle st-toggle--${settings.autoStart ? 'on' : 'off'}`} onClick={() => {
              const next = !settings.autoStart;
              update('autoStart', next);
              SetAutoStart(next);
            }} />
          </div>

          <div className="st-row">
            <span>Глобальные хеши</span>
            <button className={`st-toggle st-toggle--${settings.useGlobalHashes ? 'on' : 'off'}`} onClick={() => update('useGlobalHashes', !settings.useGlobalHashes)} />
          </div>

          <div className="st-row">
            <span>Обновление метрик</span>
            <select
              className="st-select"
              value={settings.metricsRefreshSec}
              onChange={e => update('metricsRefreshSec', +e.target.value)}
            >
              {METRICS_REFRESH_OPTIONS.map(o => (
                <option key={o.value} value={o.value}>{o.label}</option>
              ))}
            </select>
          </div>

          {deviceId && (
            <div className="st-devid-block">
              <div className="st-devid-label">ID устройства</div>
              <button className="st-devid" onClick={copyDeviceId} title="Скопировать ID устройства">
                <span className="st-devid-val">{deviceId}</span>
                {idCopied ? <IconCheck stroke={2} size={14} /> : <IconCopy stroke={2} size={14} />}
              </button>
            </div>
          )}

          <div className={`st-vk-block${locked ? ' st-locked' : ''}`}>
            <div className="st-vk-hint">
              VK закрыла анонимный вход в звонки. Вставьте cookies с remixsid (Export Cookies из creator-app или JSON из панели WDTT).
            </div>
            <div className={`st-vk-status${vkStatus?.ok ? ' st-vk-status--ok' : vkStatus?.expired ? ' st-vk-status--bad' : ''}`}>
              {vkStatus?.hint ?? 'Проверка cookies…'}
            </div>
            {vkStatus?.path && (
              <div className="st-vk-hint" style={{ marginBottom: 6 }}>{vkStatus.path}</div>
            )}
            <textarea
              className="st-vk-textarea"
              placeholder={'[{"name":"remixsid","value":"..."}] или remixsid=...; remixlang=0'}
              value={vkCookies}
              onChange={e => setVkCookies(e.target.value)}
            />
            <div className="st-vk-actions">
              <button
                className="st-vk-btn st-vk-btn--primary"
                onClick={async () => {
                  try {
                    await SaveVKCookies(vkCookies.trim());
                    setVkSaveMsg('Cookies сохранены');
                    refreshVkStatus();
                  } catch (e) {
                    setVkSaveMsg(String(e));
                  }
                }}
              >
                Сохранить cookies
              </button>
              <button
                className="st-vk-btn"
                onClick={async () => {
                  await ClearVKCookies();
                  setVkCookies('');
                  setVkSaveMsg('Cookies удалены');
                  refreshVkStatus();
                }}
              >
                Очистить
              </button>
            </div>
            <div className="st-vk-msg">{vkSaveMsg}</div>
          </div>

          <button
            className="st-hash-btn"
            style={!settings.useGlobalHashes ? { opacity: 0.35, pointerEvents: 'none' } : undefined}
            onClick={() => setHashOpen(true)}
            title={!settings.useGlobalHashes ? 'Глобальные хеши отключены — используются хеши профиля' : undefined}
          >
            <IconHash stroke={2} size={16} />
            VK Хеши ({filledHashes}/4)
          </button>

          <button
            className={`st-adv-toggle${advancedOpen ? ' st-adv-toggle--open' : ''}`}
            onClick={() => {
              if (!advancedOpen) setAdvancedConfirm(true);
              else setAdvancedOpen(false);
            }}
          >
            <span>Расширенные</span>
            <IconChevronDown stroke={2} size={16} />
          </button>

          <div className={`st-adv-body${advancedOpen ? ' st-adv-body--open' : ' st-adv-body--closed'}`}>
            <div className={`st-row${locked ? ' st-locked' : ''}`} style={{ marginTop: 10 }}>
              <span>MTU</span>
              <input
                type="number" min={576} max={1500} step={1}
                value={mtuRaw}
                className={`st-num-input${!mtuValid ? ' st-num-input--error' : ''}`}
                onChange={e => setMtuRaw(e.target.value)}
                onBlur={() => {
                  const n = Number(mtuRaw);
                  const clamped = Number.isFinite(n) ? Math.max(576, Math.min(1500, Math.round(n))) : 1280;
                  setMtuRaw(String(clamped));
                  update('mtu', clamped);
                }}
              />
            </div>
          </div>
        </div>
      </div>

      {advancedConfirm && (
        <div className="st-confirm-overlay" onClick={() => setAdvancedConfirm(false)}>
          <div className="st-confirm" onClick={e => e.stopPropagation()}>
            <div className="st-confirm-title">⚠ Расширенные настройки</div>
            <div className="st-confirm-text">
              Изменение этих параметров может нарушить работу туннеля.
              Продолжать только если вы понимаете что делаете.
            </div>
            <div className="st-confirm-actions">
              <button className="st-confirm-btn st-confirm-btn--cancel" onClick={() => setAdvancedConfirm(false)}>Отмена</button>
              <button className="st-confirm-btn st-confirm-btn--ok" onClick={() => { setAdvancedConfirm(false); setAdvancedOpen(true); }}>Продолжить</button>
            </div>
          </div>
        </div>
      )}
      {hashOpen && (
        <Hash
          hashes={settings.hashes}
          onClose={() => setHashOpen(false)}
          onSave={hashes => update('hashes', hashes)}
        />
      )}
    </>
  );
}
