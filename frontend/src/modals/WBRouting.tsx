import { useState, useEffect } from 'react';
import { IconRoute, IconPlus, IconTrash, IconChevronUp, IconChevronDown, IconX } from '@tabler/icons-react';
import { settingsStore } from '../lib/store';
import { tunnelStore } from '../lib/stores/tunnelStore';
import type { AppSettings } from '../lib/types';
import {
  type WBRoutingRule,
  type WBOutboundTag,
  DEFAULT_WB_ROUTING_RULES,
  newRuleId,
  OUTBOUND_LABELS,
} from '../lib/wbRouting';

interface Props {
  onClose: () => void;
}

function emptyRule(): WBRoutingRule {
  return {
    id: newRuleId(),
    enabled: true,
    remark: '',
    outboundTag: 'direct',
    domains: '',
    ips: '',
    port: '',
    network: '',
  };
}

export default function WBRouting({ onClose }: Props) {
  const [settings, setSettings] = useState<AppSettings>(() => settingsStore.get());
  const [rules, setRules] = useState<WBRoutingRule[]>(() =>
    settingsStore.get().wbRoutingRules?.length
      ? [...settingsStore.get().wbRoutingRules!]
      : [...DEFAULT_WB_ROUTING_RULES],
  );
  const [editId, setEditId] = useState<string | null>(null);
  const [tunnelState, setTunnelState] = useState(() => tunnelStore.get());
  useEffect(() => tunnelStore.subscribe(setTunnelState), []);
  const locked = tunnelState === 'connected' || tunnelState === 'connecting';

  const save = () => {
    const next: AppSettings = {
      ...settings,
      wbRoutingMode: 'custom',
      wbRoutingRules: rules,
    };
    settingsStore.save(next);
    setSettings(next);
    onClose();
  };

  const updateRule = (id: string, patch: Partial<WBRoutingRule>) => {
    setRules(rs => rs.map(r => (r.id === id ? { ...r, ...patch } : r)));
  };

  const moveRule = (idx: number, dir: -1 | 1) => {
    const j = idx + dir;
    if (j < 0 || j >= rules.length) return;
    setRules(rs => {
      const copy = [...rs];
      [copy[idx], copy[j]] = [copy[j], copy[idx]];
      return copy;
    });
  };

  const removeRule = (id: string) => {
    setRules(rs => rs.filter(r => r.id !== id));
    if (editId === id) setEditId(null);
  };

  const addRule = () => {
    const r = emptyRule();
    setRules(rs => [...rs, r]);
    setEditId(r.id);
  };

  const editing = editId ? rules.find(r => r.id === editId) : null;

  return (
    <>
      <style>{`
        .rt-overlay { position: fixed; inset: 0; background: var(--overlay-bg); backdrop-filter: blur(4px); display: flex; align-items: center; justify-content: center; padding: 12px; z-index: 110; animation: overlay-in 0.25s ease-out; }
        .rt-modal { background: var(--surface); border-radius: 14px; width: min(720px, calc(100vw - 16px)); max-height: calc(100vh - 24px); box-shadow: var(--shadow); border: 1px solid var(--border); display: flex; flex-direction: column; overflow: hidden; animation: modal-in 0.25s ease-out; }
        .rt-head { display: flex; align-items: center; gap: 10px; padding: 14px 16px 10px; border-bottom: 1px solid var(--border); flex-shrink: 0; }
        .rt-title { font-size: 15px; font-weight: 600; flex: 1; color: var(--text); }
        .rt-sub { font-size: 11px; color: var(--text-3); margin-top: 2px; }
        .rt-close { border: none; background: transparent; color: var(--text-3); cursor: pointer; padding: 4px; border-radius: 8px; }
        .rt-close:hover { color: var(--text); background: var(--border); }
        .rt-body { overflow: auto; flex: 1; padding: 12px 16px 16px; scrollbar-gutter: stable; scrollbar-width: thin; scrollbar-color: var(--border) transparent; }
        .rt-body::-webkit-scrollbar { width: 6px; }
        .rt-body::-webkit-scrollbar-track { background: transparent; }
        .rt-body::-webkit-scrollbar-thumb { background: var(--border); border-radius: 6px; }
        .rt-body::-webkit-scrollbar-thumb:hover { background: var(--text-3); }
        .rt-table-wrap { overflow-x: auto; border: 1px solid var(--border); border-radius: 10px; }
        .rt-table { width: 100%; border-collapse: collapse; font-size: 11px; min-width: 560px; }
        .rt-table th { text-align: left; padding: 8px 8px; color: var(--text-3); font-weight: 600; border-bottom: 1px solid var(--border); background: color-mix(in srgb, var(--border) 40%, transparent); white-space: nowrap; }
        .rt-table td { padding: 6px 8px; border-bottom: 1px solid color-mix(in srgb, var(--border) 60%, transparent); vertical-align: middle; color: var(--text-2); max-width: 140px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
        .rt-table tr:last-child td { border-bottom: none; }
        .rt-table tr:hover td { background: color-mix(in srgb, var(--border) 25%, transparent); }
        .rt-table tr.rt-row--edit td { background: color-mix(in srgb, #6d6aac 12%, transparent); }
        .rt-tag { display: inline-block; padding: 2px 6px; border-radius: 4px; font-size: 10px; font-weight: 600; }
        .rt-tag--proxy { background: color-mix(in srgb, #6d6aac 25%, transparent); color: #a89cf0; }
        .rt-tag--direct { background: color-mix(in srgb, #22c55e 20%, transparent); color: #4ade80; }
        .rt-tag--block { background: color-mix(in srgb, #ef4444 20%, transparent); color: #f87171; }
        .rt-actions { display: flex; gap: 2px; }
        .rt-icon-btn { border: none; background: transparent; color: var(--text-3); cursor: pointer; padding: 2px; border-radius: 4px; display: flex; }
        .rt-icon-btn:hover { color: var(--text); background: var(--border); }
        .rt-icon-btn:disabled { opacity: 0.35; cursor: not-allowed; }
        .rt-edit { margin-top: 12px; padding: 12px; border: 1px solid var(--border); border-radius: 10px; background: color-mix(in srgb, var(--border) 20%, transparent); }
        .rt-edit-title { font-size: 12px; font-weight: 600; margin-bottom: 10px; color: var(--text); }
        .rt-field { margin-bottom: 8px; }
        .rt-field label { display: block; font-size: 10px; color: var(--text-3); margin-bottom: 3px; text-transform: uppercase; letter-spacing: 0.04em; }
        .rt-field input, .rt-field select, .rt-field textarea { width: 100%; box-sizing: border-box; padding: 7px 9px; border-radius: 8px; border: 1px solid var(--border); background: var(--bg); color: var(--text); font-size: 12px; font-family: inherit; }
        .rt-field textarea { min-height: 52px; resize: vertical; }
        .rt-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 8px; }
        .rt-foot { display: flex; gap: 8px; justify-content: flex-end; padding: 12px 16px; border-top: 1px solid var(--border); flex-shrink: 0; }
        .rt-btn { padding: 8px 16px; border-radius: 9px; font-size: 13px; font-weight: 600; cursor: pointer; border: none; }
        .rt-btn--ghost { background: transparent; color: var(--text-2); border: 1px solid var(--border); }
        .rt-btn--primary { background: #6d6aac; color: #fff; }
        .rt-btn:disabled { opacity: 0.45; cursor: not-allowed; }
        .rt-add { display: flex; align-items: center; gap: 6px; margin-top: 10px; font-size: 12px; padding: 8px 12px; border-radius: 8px; border: 1px dashed var(--border); background: transparent; color: var(--text-2); cursor: pointer; width: 100%; justify-content: center; }
        .rt-add:hover { border-color: #6d6aac; color: var(--text); }
        .rt-locked { font-size: 11px; color: #f59e0b; margin-bottom: 8px; }
      `}</style>
      <div className="rt-overlay" onClick={e => { if (e.target === e.currentTarget) onClose(); }}>
        <div className="rt-modal" onClick={e => e.stopPropagation()}>
          <div className="rt-head">
            <IconRoute size={20} stroke={2} color="#6d6aac" />
            <div style={{ flex: 1 }}>
              <div className="rt-title">Маршрутизация WB</div>
              <div className="rt-sub">Правила xray · как v2rayN / панель</div>
            </div>
            <button type="button" className="rt-close" onClick={onClose} aria-label="Закрыть">
              <IconX size={18} />
            </button>
          </div>

          <div className="rt-body">
            {locked && (
              <div className="rt-locked">Туннель активен — правила применятся при следующем подключении.</div>
            )}

            <div className="rt-table-wrap">
              <table className="rt-table">
                <thead>
                  <tr>
                    <th style={{ width: 28 }} />
                    <th>Примечание</th>
                    <th>Outbound</th>
                    <th>Domain</th>
                    <th>IP</th>
                    <th>Port</th>
                    <th>Net</th>
                    <th style={{ width: 72 }} />
                  </tr>
                </thead>
                <tbody>
                  {rules.map((r, idx) => (
                    <tr
                      key={r.id}
                      className={editId === r.id ? 'rt-row--edit' : ''}
                      onClick={() => setEditId(r.id)}
                      style={{ cursor: 'pointer', opacity: r.enabled ? 1 : 0.45 }}
                    >
                      <td onClick={e => e.stopPropagation()}>
                        <input
                          type="checkbox"
                          checked={r.enabled}
                          disabled={locked}
                          onChange={e => updateRule(r.id, { enabled: e.target.checked })}
                        />
                      </td>
                      <td title={r.remark}>{r.remark || '—'}</td>
                      <td>
                        <span className={`rt-tag rt-tag--${r.outboundTag}`}>
                          {OUTBOUND_LABELS[r.outboundTag]}
                        </span>
                      </td>
                      <td title={r.domains}>{r.domains || '—'}</td>
                      <td title={r.ips}>{r.ips || '—'}</td>
                      <td>{r.port || '—'}</td>
                      <td>{r.network || '—'}</td>
                      <td onClick={e => e.stopPropagation()}>
                        <div className="rt-actions">
                          <button type="button" className="rt-icon-btn" disabled={locked || idx === 0} onClick={() => moveRule(idx, -1)} title="Вверх">
                            <IconChevronUp size={14} />
                          </button>
                          <button type="button" className="rt-icon-btn" disabled={locked || idx === rules.length - 1} onClick={() => moveRule(idx, 1)} title="Вниз">
                            <IconChevronDown size={14} />
                          </button>
                          <button type="button" className="rt-icon-btn" disabled={locked} onClick={() => removeRule(r.id)} title="Удалить">
                            <IconTrash size={14} />
                          </button>
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>

            <button type="button" className="rt-add" disabled={locked} onClick={addRule}>
              <IconPlus size={16} /> Добавить правило
            </button>

            {editing && (
              <div className="rt-edit">
                <div className="rt-edit-title">Редактирование: {editing.remark || 'новое правило'}</div>
                <div className="rt-field">
                  <label>Примечание</label>
                  <input
                    value={editing.remark}
                    disabled={locked}
                    onChange={e => updateRule(editing.id, { remark: e.target.value })}
                    placeholder="Обход LAN, RU direct…"
                  />
                </div>
                <div className="rt-grid">
                  <div className="rt-field">
                    <label>Outbound</label>
                    <select
                      value={editing.outboundTag}
                      disabled={locked}
                      onChange={e => updateRule(editing.id, { outboundTag: e.target.value as WBOutboundTag })}
                    >
                      {(Object.keys(OUTBOUND_LABELS) as WBOutboundTag[]).map(k => (
                        <option key={k} value={k}>{OUTBOUND_LABELS[k]}</option>
                      ))}
                    </select>
                  </div>
                  <div className="rt-field">
                    <label>Network</label>
                    <select
                      value={editing.network}
                      disabled={locked}
                      onChange={e => updateRule(editing.id, { network: e.target.value as WBRoutingRule['network'] })}
                    >
                      <option value="">любой</option>
                      <option value="tcp">tcp</option>
                      <option value="udp">udp</option>
                      <option value="tcp,udp">tcp,udp</option>
                    </select>
                  </div>
                </div>
                <div className="rt-field">
                  <label>Domain (через запятую)</label>
                  <textarea
                    value={editing.domains}
                    disabled={locked}
                    onChange={e => updateRule(editing.id, { domains: e.target.value })}
                    placeholder="geosite:ru, domain:example.com"
                  />
                </div>
                <div className="rt-field">
                  <label>IP (через запятую)</label>
                  <textarea
                    value={editing.ips}
                    disabled={locked}
                    onChange={e => updateRule(editing.id, { ips: e.target.value })}
                    placeholder="geoip:ru, 192.168.0.0/16"
                  />
                </div>
                <div className="rt-field">
                  <label>Port</label>
                  <input
                    value={editing.port}
                    disabled={locked}
                    onChange={e => updateRule(editing.id, { port: e.target.value })}
                    placeholder="443"
                  />
                </div>
              </div>
            )}
          </div>

          <div className="rt-foot">
            <button type="button" className="rt-btn rt-btn--ghost" onClick={onClose}>Отмена</button>
            <button type="button" className="rt-btn rt-btn--primary" onClick={save}>Сохранить</button>
          </div>
        </div>
      </div>
    </>
  );
}
