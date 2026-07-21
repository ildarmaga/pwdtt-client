import { useState, useEffect, type CSSProperties } from 'react';
import { IconSettings2, IconHash, IconChevronDown } from '@tabler/icons-react';
import Hash from './Hash';
import { settingsStore, serverStore } from '../lib/store';
import { selectedServerStore } from '../lib/stores/selectedServerStore';
import { tunnelStore } from '../lib/stores/tunnelStore';
import { updateStore } from '../lib/stores/updateStore';
import type { AppSettings, TunnelProtocol, Server } from '../lib/types';
import { METRICS_REFRESH_OPTIONS } from '../lib/types';
import { useMobileUI } from '../lib/useMobileUI';
import { useTunnelProtocol, isVkProtocol } from '../lib/useTunnelProtocol';
import { saveServerProfile } from '../lib/utils/profileSync';
import {
  SetTrayEnabled, SetAutoStart, GetAutoStart,
  GetVKCookiesStatus, GetVKCookiesRaw, SaveVKCookies, ClearVKCookies, GetVKUseCookies, SetVKUseCookies,
  CheckForUpdate, DownloadAndApplyUpdate, CancelUpdateDownload, GetUpdateDownloadState, IsUpdateDownloading,
  GetDataDir,
} from '../../wailsjs/go/backend/App';
import type { backend } from '../../wailsjs/go/models';

interface Props {
  onClose: () => void;
}

const PROTOCOL_BADGE: Record<TunnelProtocol, { label: string; color: string }> = {
  vk: { label: 'VK', color: '#2787f5' },
  wb: { label: 'WB', color: '#6d6aac' },
};

function NumberStepper({ value, min, max, step, disabled, onChange }: {
  value: number; min: number; max: number; step: number;
  disabled?: boolean; onChange: (v: number) => void;
}) {
  const [raw, setRaw] = useState(String(value));

  useEffect(() => {
    setRaw(String(value));
  }, [value]);

  const commit = (text: string) => {
    const n = Number(text);
    const clamped = Number.isFinite(n)
      ? Math.max(min, Math.min(max, Math.round(n)))
      : value;
    setRaw(String(clamped));
    if (clamped !== value) {
      onChange(clamped);
    }
  };

  const bump = (delta: number) => {
    const next = Math.max(min, Math.min(max, value + delta));
    setRaw(String(next));
    onChange(next);
  };

  return (
    <div className={`st-stepper${disabled ? ' st-stepper--disabled' : ''}`}>
      <button type="button" disabled={disabled || value <= min} onClick={() => bump(-step)}>−</button>
      <input
        type="number"
        min={min}
        max={max}
        step={step}
        disabled={disabled}
        className="st-num-input st-num-input--stepper"
        value={raw}
        onChange={e => setRaw(e.target.value)}
        onBlur={() => commit(raw)}
        onKeyDown={e => {
          if (e.key === 'Enter') {
            e.currentTarget.blur();
          }
        }}
      />
      <button type="button" disabled={disabled || value >= max} onClick={() => bump(step)}>+</button>
    </div>
  );
}

export default function Settings({ onClose }: Props) {
  const [settings, setSettings] = useState<AppSettings>(() => settingsStore.get());
  const [hashOpen, setHashOpen] = useState(false);
  const [mtuRaw, setMtuRaw] = useState(String(settingsStore.get().mtu ?? 1280));
  const mtuValid = (() => { const n = Number(mtuRaw); return Number.isInteger(n) && n >= 576 && n <= 1500; })();
  const [tunnelState, setTunnelState] = useState(() => tunnelStore.get());
  useEffect(() => tunnelStore.subscribe(setTunnelState), []);
  const locked = tunnelState === 'connected' || tunnelState === 'connecting' || tunnelState === 'disconnecting';
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const [advancedConfirm, setAdvancedConfirm] = useState(false);
  const [vkCookies, setVkCookies] = useState('');
  const [vkStatus, setVkStatus] = useState<backend.VKCookiesStatus | null>(null);
  const [vkSaveMsg, setVkSaveMsg] = useState('');
  const [vkUseCookies, setVkUseCookies] = useState(false);
  const [updateMsg, setUpdateMsg] = useState('');
  const [updateChecking, setUpdateChecking] = useState(false);
  const [updateInfo, setUpdateInfo] = useState<backend.UpdateInfo | null>(null);
  const [updateSnap, setUpdateSnap] = useState(() => updateStore.get());
  useEffect(() => updateStore.subscribe(setUpdateSnap), []);
  const updateApplying = updateSnap.phase === 'downloading' || updateSnap.phase === 'applying';
  const updateReady = updateSnap.phase === 'ready';
  const updateProgress = updateSnap.percent;
  const updateProgressMsg = updateSnap.message;
  const [dataDir, setDataDir] = useState('');
  const isMobile = useMobileUI();
  const protocol = useTunnelProtocol();
  const isVk = isVkProtocol(protocol);
  const isWb = !isVk;
  const [profileHashes, setProfileHashes] = useState<[string, string, string, string]>(['', '', '', '']);
  const [hashEditGlobal, setHashEditGlobal] = useState(true);

  const refreshProfileHashes = () => {
    const all = serverStore.getAll();
    const srv = all.find(s => s.id === selectedServerStore.getId()) ?? all[0];
    if (!srv) {
      setProfileHashes(['', '', '', '']);
      return;
    }
    const h = srv.hashes ?? ['', '', '', ''];
    setProfileHashes([h[0] ?? '', h[1] ?? '', h[2] ?? '', h[3] ?? '']);
  };

  useEffect(refreshProfileHashes, []);
  useEffect(() => {
    void GetDataDir().then(setDataDir).catch(() => setDataDir(''));
  }, []);

  const refreshVkStatus = () => {
    GetVKCookiesStatus().then(setVkStatus).catch(() => setVkStatus(null));
    GetVKUseCookies().then(setVkUseCookies).catch(() => setVkUseCookies(false));
    GetVKCookiesRaw()
      .then(raw => setVkCookies(raw ?? ''))
      .catch(() => { /* keep textarea as-is */ });
  };

  useEffect(() => { if (isVk) refreshVkStatus(); }, [isVk]);

  useEffect(() => {
    void (async () => {
      try {
        if (await IsUpdateDownloading()) {
          const st = await GetUpdateDownloadState();
          updateStore.syncFromBackend(st);
        }
      } catch { /* dev / old bindings */ }
    })();
  }, []);

  useEffect(() => settingsStore.subscribe(s => {
    setSettings(prev => prev.tunnelProtocol === s.tunnelProtocol ? prev : { ...prev, tunnelProtocol: s.tunnelProtocol });
  }), []);

  // Sync autoStart from backend on open (только десктоп)
  useEffect(() => {
    if (isMobile) return;
    GetAutoStart().then(v => {
      if (v !== settings.autoStart) update('autoStart', v);
    });
  }, [isMobile]);

  const update = <K extends keyof AppSettings>(key: K, value: AppSettings[K]) => {
    setSettings(s => ({ ...s, [key]: value }));
  };

  const updateAndSave = <K extends keyof AppSettings>(key: K, value: AppSettings[K]) => {
    setSettings(s => {
      const next = { ...s, [key]: value };
      settingsStore.save(next);
      return next;
    });
  };

  const handleClose = () => {
    const n = Number(mtuRaw);
    const mtu = mtuValid ? n : settings.mtu;
    settingsStore.save({ ...settings, mtu });
    onClose();
  };

  const filledHashes = settings.useGlobalHashes
    ? settings.hashes.filter(h => h.trim()).length
    : profileHashes.filter(h => h.trim()).length;
  const hashButtonLabel = settings.useGlobalHashes ? 'VK Хеши глобальные' : 'VK Хеши профиля';
  const powerMax = Math.max(9, filledHashes * 27);
  const protoBadge = PROTOCOL_BADGE[protocol];
  const wbManualProxy = settings.wbProxyAuth === 'manual';

  return (
    <>
      <style>{`
        .st-overlay { position: fixed; inset: 0; background: var(--overlay-bg); display: flex; align-items: center; justify-content: center; padding: 16px; z-index: 100; animation: overlay-in 0.3s ease-out; overflow: hidden; }
        .st-modal { background: var(--surface); border-radius: 12px; padding: 12px 6px 12px 14px; width: min(400px, calc(100vw - 24px)); max-width: calc(100vw - 24px); max-height: calc(100vh - 32px); box-shadow: var(--shadow); animation: modal-in 0.3s ease-out; border: 1px solid var(--border); overflow: hidden; flex-shrink: 0; display: flex; flex-direction: column; box-sizing: border-box; }
        .st-modal-body { overflow-x: hidden; overflow-y: auto; flex: 1; min-height: 0; min-width: 0; padding-right: 12px; scrollbar-gutter: stable both-edges; }
        .st-modal-body::-webkit-scrollbar { width: 5px; }
        .st-modal-body::-webkit-scrollbar-track { background: transparent; margin: 4px 0; }
        .st-modal-body::-webkit-scrollbar-thumb { background: var(--border); border-radius: 5px; }
        .st-header { display: flex; align-items: center; gap: 8px; margin-bottom: 8px; color: var(--text); }
        .st-title { font-size: 14px; font-weight: 600; flex: 1; color: var(--text); }
        .st-protocol-badge {
          font-size: 9px; font-weight: 700; letter-spacing: 0.05em; text-transform: uppercase;
          padding: 2px 7px; border-radius: 5px; background: color-mix(in srgb, var(--proto-color) 18%, transparent);
          color: var(--proto-color); border: 1px solid color-mix(in srgb, var(--proto-color) 35%, transparent);
        }
        .st-section-title { font-size: 10px; font-weight: 700; letter-spacing: 0.06em; text-transform: uppercase; color: var(--text-3); margin: 10px 0 2px; }
        .st-section-title:first-child { margin-top: 0; }
        .st-stepper { display: flex; align-items: center; gap: 4px; }
        .st-stepper button {
          width: 22px; height: 22px; border: 1px solid var(--border); border-radius: 5px;
          background: var(--seg-bg); color: var(--text); font-size: 13px; line-height: 1; cursor: pointer;
          padding: 0; display: inline-flex; align-items: center; justify-content: center;
        }
        .st-stepper button:disabled { opacity: 0.35; cursor: default; }
        .st-stepper span { min-width: 24px; text-align: center; font-size: 12px; font-weight: 600; }
        .st-stepper--disabled { opacity: 0.45; pointer-events: none; }
        .st-num-input--stepper { width: 48px; padding: 3px 4px; text-align: center; font-size: 12px; -moz-appearance: textfield; }
        .st-num-input--stepper::-webkit-outer-spin-button,
        .st-num-input--stepper::-webkit-inner-spin-button { -webkit-appearance: none; margin: 0; }
        .st-field { margin-top: 6px; }
        .st-field label { display: block; font-size: 11px; color: var(--text-3); margin-bottom: 3px; }
        .st-input {
          width: 100%; padding: 6px 8px; border: 1px solid var(--border); border-radius: 6px;
          font-size: 12px; font-family: 'Geist', sans-serif; background: var(--input-bg); color: var(--text);
          box-sizing: border-box; outline: none;
        }
        .st-input:focus { border-color: var(--accent); }
        .st-close { background: none; border: none; cursor: pointer; font-size: 15px; color: var(--text-3); line-height: 1; padding: 0; }
        .st-close:hover { color: var(--text); }
        .st-row { display: flex; align-items: center; justify-content: space-between; padding: 5px 0; border-bottom: 1px solid var(--border-2); font-size: 12px; color: var(--text); min-width: 0; gap: 8px; }
        .st-row--stack { flex-direction: column; align-items: stretch; gap: 4px; min-width: 0; }
        .st-row-head { display: flex; align-items: center; justify-content: space-between; gap: 10px; min-width: 0; width: 100%; }
        .st-row-hint { font-size: 10px; color: var(--text-3); line-height: 1.35; overflow-wrap: anywhere; word-break: break-word; }
        .st-row:last-of-type { border-bottom: none; }
        .st-toggle { width: 36px; height: 20px; border-radius: 50px; border: none; cursor: pointer; position: relative; transition: background 0.2s; flex-shrink: 0; }
        .st-toggle--on { background: var(--accent); }
        .st-toggle--off { background: var(--seg-bg); }
        .st-toggle::after { content: ''; position: absolute; width: 14px; height: 14px; border-radius: 50%; top: 3px; transition: left 0.2s; }
        .st-toggle--on::after { background: var(--accent-fg); left: 19px; }
        .st-toggle--off::after { background: var(--text-3); left: 3px; }
        .st-seg { display: flex; background: var(--seg-bg); border-radius: 6px; padding: 2px; gap: 2px; }
        .st-seg-btn { padding: 3px 10px; border: none; border-radius: 5px; font-size: 11px; font-weight: 600; cursor: pointer; transition: background 0.15s, color 0.15s; background: transparent; color: var(--seg-text); }
        .st-seg-btn--active { background: var(--accent); color: var(--accent-fg); }
        .st-slider-wrap { padding: 2px 0 6px; border-bottom: 1px solid var(--border-2); }
        .st-slider-label { display: flex; justify-content: space-between; font-size: 12px; color: var(--text); margin-bottom: 6px; }
        .st-slider { width: 100%; -webkit-appearance: none; appearance: none; height: 3px; border-radius: 2px; outline: none; cursor: pointer; background: linear-gradient(to right, var(--accent) calc(var(--v) * 1%), var(--border) calc(var(--v) * 1%)); }
        .st-slider::-webkit-slider-thumb { -webkit-appearance: none; width: 14px; height: 14px; border-radius: 50%; background: var(--surface); border: 2px solid var(--accent); cursor: pointer; }
        .st-num-input { width: 64px; padding: 3px 8px; border: 1px solid var(--border); border-radius: 6px; font-size: 12px; font-family: 'Geist', sans-serif; text-align: right; outline: none; background: var(--input-bg); color: var(--text); transition: border-color 0.15s; }
        .st-num-input:focus { border-color: var(--accent); }
        .st-num-input--error { border-color: #ef4444; }
        .st-select { padding: 3px 6px; border: 1px solid var(--border); border-radius: 6px; font-size: 12px; font-family: 'Geist', sans-serif; background: var(--input-bg); color: var(--text); outline: none; cursor: pointer; max-width: 130px; }
        .st-select:focus { border-color: var(--accent); }
        .st-hash-btn { width: 100%; margin-top: 8px; padding: 7px; border: 1px solid var(--border); border-radius: 8px; background: var(--surface); color: var(--text); font-size: 12px; font-family: 'Geist', sans-serif; font-weight: 600; cursor: pointer; display: flex; align-items: center; justify-content: center; gap: 6px; }
        .st-locked { opacity: 0.4; pointer-events: none; }
        .st-lock-hint { font-size: 10px; color: var(--text-3); margin-bottom: 4px; text-align: center; }
        .st-adv-toggle { width: 100%; display: flex; align-items: center; justify-content: space-between; background: none; border: none; border-top: 1px solid var(--border-2); padding: 6px 0 0; cursor: pointer; font-size: 12px; font-weight: 600; color: var(--text-3); font-family: 'Geist', sans-serif; margin-top: 2px; }
        .st-adv-toggle svg { transition: transform 0.2s; }
        .st-adv-toggle--open svg { transform: rotate(180deg); }
        .st-adv-body { overflow: hidden; transition: max-height 0.25s ease, opacity 0.2s; }
        .st-adv-body--open { max-height: 300px; opacity: 1; }
        .st-adv-body--closed { max-height: 0; opacity: 0; pointer-events: none; }
        .st-confirm-overlay { position: fixed; inset: 0; background: var(--overlay-bg); display: flex; align-items: center; justify-content: center; z-index: 200; }
        .st-confirm { background: var(--surface); border-radius: 12px; padding: 16px 14px 14px; width: 300px; max-width: 92vw; box-shadow: var(--shadow); border: 1px solid var(--border); }
        .st-confirm-title { font-size: 13px; font-weight: 700; color: var(--text); margin-bottom: 6px; }
        .st-confirm-text { font-size: 12px; color: var(--text-2); line-height: 1.45; margin-bottom: 14px; }
        .st-confirm-actions { display: flex; gap: 6px; justify-content: flex-end; }
        .st-confirm-btn { padding: 6px 14px; border-radius: 6px; border: none; font-size: 12px; font-family: 'Geist', sans-serif; font-weight: 600; cursor: pointer; }
        .st-confirm-btn--cancel { background: var(--seg-bg); color: var(--text); }
        .st-confirm-btn--ok { background: var(--accent); color: var(--accent-fg); }
        .st-vk-block { margin-top: 8px; padding-top: 8px; border-top: 1px solid var(--border-2); }
        .st-vk-hint { font-size: 10px; color: var(--text-3); line-height: 1.4; margin-bottom: 6px; }
        .st-vk-status { font-size: 11px; margin-bottom: 6px; color: var(--text-2); }
        .st-vk-status--ok { color: #22c55e; }
        .st-vk-status--bad { color: #ef4444; }
        .st-vk-textarea { width: 100%; max-width: 100%; min-height: 64px; resize: vertical; padding: 6px 8px; border: 1px solid var(--border); border-radius: 6px; font-size: 10px; font-family: 'Geist Mono', ui-monospace, monospace; background: var(--input-bg); color: var(--text); box-sizing: border-box; overflow-wrap: anywhere; word-break: break-all; }
        .st-vk-actions { display: flex; gap: 6px; margin-top: 6px; min-width: 0; }
        .st-vk-btn { flex: 1; min-width: 0; padding: 6px 8px; border-radius: 6px; border: 1px solid var(--border); background: var(--surface); color: var(--text); font-size: 11px; font-weight: 600; cursor: pointer; }
        .st-vk-btn--primary { background: var(--accent); color: var(--accent-fg); border-color: var(--accent); }
        .st-vk-msg { font-size: 10px; color: var(--text-3); margin-top: 4px; min-height: 14px; }
        .st-vk-msg--warn { color: #f59e0b; }
        .st-upd-progress { margin-top: 6px; }
        .st-upd-progress-track { height: 4px; border-radius: 2px; background: var(--seg-bg); overflow: hidden; }
        .st-upd-progress-bar { height: 100%; border-radius: 2px; background: var(--accent); transition: width 0.2s ease; }
        .st-upd-progress-label { font-size: 10px; color: var(--text-3); margin-top: 3px; }
      `}</style>
      <div className="st-overlay" onClick={handleClose}>
        <div className="st-modal" onClick={e => e.stopPropagation()}>
          <div className="st-header">
            <IconSettings2 stroke={2} size={16} />
            <span className="st-title">Настройки</span>
            <span className="st-protocol-badge" style={{ '--proto-color': protoBadge.color } as CSSProperties}>
              {protoBadge.label}
            </span>
            <button className="st-close" onClick={handleClose}>✕</button>
          </div>

          {locked && <div className="st-lock-hint">Недоступно во время подключения</div>}

          <div className="st-modal-body">
          <div className="st-section-title">Приложение</div>
          {dataDir && (
            <div className="st-hint" style={{ marginBottom: 8, wordBreak: 'break-all', fontSize: 11, opacity: 0.75 }}>
              Данные: {dataDir}
            </div>
          )}

          {!isMobile && (
            <>
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
            </>
          )}

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

          <div className="st-row st-row--stack">
            <div className="st-row-head">
              <span>Обновления</span>
            </div>
            <div className="st-vk-actions">
              <button
                type="button"
                className="st-vk-btn"
                disabled={updateChecking}
                onClick={async () => {
                  setUpdateChecking(true);
                  setUpdateMsg('');
                  try {
                    const info = await CheckForUpdate();
                    setUpdateInfo(info);
                    if (info.error && !info.latest) {
                      setUpdateMsg(info.error);
                      return;
                    }
                    if (info.hasUpdate) {
                      setUpdateMsg(`Доступна ${info.latest}`);
                    } else {
                      setUpdateMsg(info.latest ? `Актуальная версия (${info.latest})` : 'Обновлений нет');
                    }
                  } catch (e) {
                    setUpdateMsg(String(e));
                  } finally {
                    setUpdateChecking(false);
                  }
                }}
              >
                {updateChecking ? 'Проверка…' : 'Проверить'}
              </button>
              {updateInfo?.hasUpdate && (
                <button
                  type="button"
                  className="st-vk-btn st-vk-btn--primary"
                  disabled={updateApplying || updateReady}
                  onClick={async () => {
                    if (updateStore.isActive()) {
                      setUpdateMsg('Обновление уже скачивается');
                      return;
                    }
                    updateStore.startDownload();
                    setUpdateMsg('');
                    try {
                      const res = await DownloadAndApplyUpdate();
                      if (res.ok) {
                        setUpdateMsg(res.message || 'Перезапуск…');
                      } else {
                        setUpdateMsg(res.message || 'Ошибка');
                        updateStore.finish();
                      }
                    } catch (e) {
                      setUpdateMsg(String(e));
                      updateStore.finish();
                    }
                  }}
                >
                  {updateReady ? 'Ждёт отключения VPN' : updateApplying ? (updateProgress > 0 ? `${updateProgress}%` : 'Скачивание…') : (locked ? `Скачать ${updateInfo.latest}` : `Установить ${updateInfo.latest}`)}
                </button>
              )}
              {updateApplying && (
                <button
                  type="button"
                  className="st-vk-btn"
                  onClick={async () => {
                    try {
                      await CancelUpdateDownload();
                    } catch { /* ignore */ }
                    updateStore.finish();
                    setUpdateMsg('Скачивание отменено — можно запустить снова');
                  }}
                >
                  Отменить
                </button>
              )}
            </div>
            {(updateApplying || updateReady) && (
              <div className="st-upd-progress">
                <div className="st-upd-progress-track">
                  <div className="st-upd-progress-bar" style={{ width: `${Math.max(updateProgress, 2)}%` }} />
                </div>
                <div className="st-upd-progress-label">{updateProgressMsg || 'Скачивание…'}</div>
              </div>
            )}
            {updateMsg && <div className={`st-vk-msg${locked && updateInfo?.hasUpdate ? ' st-vk-msg--warn' : ''}`}>{updateMsg}</div>}
          </div>

          {isVk && (
            <>
          <div className="st-section-title">VK · TURN</div>

          {settings.useGlobalHashes ? (
            <div className={`st-slider-wrap${locked ? ' st-locked' : ''}`}>
              <div className="st-slider-label"><span>Мощность</span><span>{settings.power}</span></div>
              <input
                type="range" min={9} max={powerMax} step={9} value={Math.min(settings.power, powerMax)}
                className="st-slider"
                style={{ '--v': Math.round((Math.min(settings.power, powerMax) - 9) / Math.max(powerMax - 9, 1) * 100) } as CSSProperties}
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
            <div>
              <span>Глобальные хеши</span>
              <div className="st-row-hint" style={{ marginTop: 4 }}>
                {settings.useGlobalHashes
                  ? 'Хеши из Настроек. Профиль подписки игнорируется.'
                  : 'Хеши берутся из профиля / подписки (рекомендуется).'}
              </div>
            </div>
            <button className={`st-toggle st-toggle--${settings.useGlobalHashes ? 'on' : 'off'}`} onClick={() => update('useGlobalHashes', !settings.useGlobalHashes)} />
          </div>

          <button
            className="st-hash-btn"
            onClick={() => {
              setHashEditGlobal(settings.useGlobalHashes);
              setHashOpen(true);
            }}
          >
            <IconHash stroke={2} size={16} />
            {hashButtonLabel} ({filledHashes}/4)
          </button>

          <div className={`st-vk-block${locked ? ' st-locked' : ''}`}>
            <div className="st-row" style={{ marginBottom: 10 }}>
              <span>VK cookies (remixsid)</span>
              <button
                className={`st-toggle st-toggle--${vkUseCookies ? 'on' : 'off'}`}
                disabled={locked}
                onClick={async () => {
                  const next = !vkUseCookies;
                  try {
                    await SetVKUseCookies(next);
                    setVkUseCookies(next);
                    refreshVkStatus();
                  } catch (e) {
                    setVkSaveMsg(String(e));
                  }
                }}
              />
            </div>
            {vkStatus === null ? (
              <div className="st-vk-status">Проверка…</div>
            ) : (
              <div className={`st-vk-status${vkStatus.ok ? ' st-vk-status--ok' : vkStatus.expired || !vkStatus.ok ? ' st-vk-status--bad' : ''}`}>
                {vkStatus.hint || (vkStatus.ok ? 'Cookies действительны' : 'Cookies не заданы')}
              </div>
            )}
            {vkStatus?.path && (
              <div className="st-vk-hint" style={{ marginBottom: 8 }}>{vkStatus.path}</div>
            )}
            <div className="st-vk-hint" style={{ marginBottom: vkUseCookies ? 8 : 0 }}>
              Вход через VK — на главном экране (режим Cookies / Гость).
            </div>
            {vkUseCookies && (
              <>
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
              </>
            )}
            <div className="st-vk-msg">{vkSaveMsg}</div>
          </div>

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
            </>
          )}

          {isWb && (
            <>
          <div className="st-section-title">WB Stream · SOCKS</div>

          <div className={`st-row${locked ? ' st-locked' : ''}`}>
            <span>SOCKS порт</span>
            <NumberStepper
              value={settings.wbSocksPort || 10809}
              min={1024} max={65535} step={1}
              disabled={locked}
              onChange={v => updateAndSave('wbSocksPort', v)}
            />
          </div>

          <div className="st-section-title">VP8</div>

          <div className={`st-row${locked ? ' st-locked' : ''}`}>
            <span>FPS</span>
            <NumberStepper
              value={settings.wbFps}
              min={1} max={120} step={1}
              disabled={locked}
              onChange={v => updateAndSave('wbFps', v)}
            />
          </div>

          <div className={`st-row${locked ? ' st-locked' : ''}`}>
            <span>Batch</span>
            <NumberStepper
              value={settings.wbBatch}
              min={1} max={256} step={1}
              disabled={locked}
              onChange={v => updateAndSave('wbBatch', v)}
            />
          </div>

          <div className={`st-row${locked ? ' st-locked' : ''}`} style={{ borderBottom: 'none' }}>
            <span title="Второй VP8 (ScreenShare). Выкл — один трек (стабильнее). Вкл — шардинг по 2 (быстрее, риск потерь на SFU).">
              Dual-track
            </span>
            <button
              type="button"
              className={`st-toggle st-toggle--${settings.wbDualTrack ? 'on' : 'off'}`}
              disabled={locked}
              onClick={() => updateAndSave('wbDualTrack', !settings.wbDualTrack)}
            />
          </div>

          <div className="st-section-title">Proxy</div>

          <div className={`st-row${locked ? ' st-locked' : ''}`}>
            <span>Авторизация</span>
            <div className="st-seg">
              <button
                type="button"
                className={`st-seg-btn${settings.wbProxyAuth === 'auto' ? ' st-seg-btn--active' : ''}`}
                disabled={locked}
                onClick={() => update('wbProxyAuth', 'auto')}
              >
                Авто
              </button>
              <button
                type="button"
                className={`st-seg-btn${settings.wbProxyAuth === 'manual' ? ' st-seg-btn--active' : ''}`}
                disabled={locked}
                onClick={() => update('wbProxyAuth', 'manual')}
              >
                Вручную
              </button>
            </div>
          </div>

          {!wbManualProxy ? (
            <div className="st-vk-hint">Логин и пароль генерируются при запуске</div>
          ) : (
            <div className={`${locked ? ' st-locked' : ''}`}>
              <div className="st-field">
                <label htmlFor="wb-proxy-user">Логин</label>
                <input
                  id="wb-proxy-user"
                  className="st-input"
                  placeholder="username"
                  autoComplete="off"
                  value={settings.wbProxyUser}
                  onChange={e => update('wbProxyUser', e.target.value)}
                />
              </div>
              <div className="st-field">
                <label htmlFor="wb-proxy-pass">Пароль</label>
                <input
                  id="wb-proxy-pass"
                  type="password"
                  className="st-input"
                  placeholder="password"
                  autoComplete="off"
                  value={settings.wbProxyPass}
                  onChange={e => update('wbProxyPass', e.target.value)}
                />
              </div>
            </div>
          )}
            </>
          )}
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
          hashes={hashEditGlobal ? settings.hashes : profileHashes}
          onClose={() => setHashOpen(false)}
          onSave={async hashes => {
            if (hashEditGlobal) {
              update('hashes', hashes);
              return;
            }
            const all = serverStore.getAll();
            const srv = all.find(s => s.id === selectedServerStore.getId()) ?? all[0];
            if (!srv) return;
            const updated: Server = { ...srv, hashes };
            serverStore.update(updated);
            await saveServerProfile(updated).catch(() => {});
            setProfileHashes(hashes);
          }}
        />
      )}
    </>
  );
}
