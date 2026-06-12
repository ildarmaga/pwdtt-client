import { useEffect, useState } from 'react';
import { subscribeDevMetrics, type DevMetricEntry } from '../lib/dev/devMetricsLog';
import { isBrowserDev } from '../lib/dev/mockWails';

function fmtTime(ts: number) {
  return new Date(ts).toLocaleTimeString('ru-RU', { hour: '2-digit', minute: '2-digit', second: '2-digit' });
}

export default function DevMetricsPanel() {
  const [open, setOpen] = useState(true);
  const [entries, setEntries] = useState<DevMetricEntry[]>([]);

  useEffect(() => {
    if (!isBrowserDev) return;
    return subscribeDevMetrics(setEntries);
  }, []);

  if (!isBrowserDev) return null;

  return (
    <div style={{
      position: 'fixed', right: 12, bottom: 12, zIndex: 9998,
      width: open ? 420 : 160, maxWidth: 'calc(100vw - 24px)',
      background: 'var(--surface)', border: '1px solid var(--border)',
      borderRadius: 10, boxShadow: 'var(--shadow)', fontSize: 11,
      color: 'var(--text-2)', overflow: 'hidden',
    }}>
      <button
        type="button"
        onClick={() => setOpen(o => !o)}
        style={{
          width: '100%', border: 'none', background: 'var(--bg-2)', color: 'var(--text)',
          padding: '8px 10px', cursor: 'pointer', textAlign: 'left', fontWeight: 600,
        }}
      >
        {open ? '▾ Метрики sub (dev)' : '▸ Метрики sub'}
      </button>
      {open && (
        <div style={{ maxHeight: 220, overflow: 'auto', padding: '6px 8px' }}>
          {entries.length === 0 && (
            <div style={{ padding: 8, color: 'var(--text-3)' }}>
              Импортируйте sub или wdtt:// с полем sub, затем «Подключить» — здесь будут HEAD/GET запросы.
            </div>
          )}
          {entries.map((e, i) => (
            <div key={`${e.ts}-${i}`} style={{
              padding: '6px 0', borderBottom: '1px solid var(--border-2)',
              color: e.ok ? 'var(--text-2)' : '#ef4444',
            }}>
              <div><strong>{e.method}</strong> {fmtTime(e.ts)}</div>
              <div style={{ wordBreak: 'break-all' }}>{e.url}</div>
              <div style={{ color: 'var(--text-3)' }}>{e.detail}</div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
