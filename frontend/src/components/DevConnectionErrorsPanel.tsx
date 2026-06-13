import { useState, type CSSProperties } from 'react';
import { isBrowserDev } from '../lib/dev/mockWails';
import { connectionErrorStore } from '../lib/stores/connectionErrorStore';
import { connectionErrorSamples } from '../lib/utils/connectionErrors';

export default function DevConnectionErrorsPanel() {
  const [open, setOpen] = useState(true);
  const samples = connectionErrorSamples();

  if (!isBrowserDev) return null;

  return (
    <div style={{
      position: 'fixed', left: 12, bottom: 250, zIndex: 9998,
      width: open ? 220 : 148,
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
        {open ? '▾ Ошибки (dev)' : '▸ Ошибки'}
      </button>
      {open && (
        <div style={{ padding: '8px 10px', display: 'flex', flexDirection: 'column', gap: 6 }}>
          <div style={{ color: 'var(--text-3)', lineHeight: 1.35, marginBottom: 2 }}>
            Баннер на главной (~30 сек)
          </div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 4, maxHeight: 280, overflowY: 'auto' }}>
            {samples.map(s => (
              <button
                key={s.raw}
                type="button"
                onClick={() => connectionErrorStore.show(s.raw)}
                style={btn}
                title={s.raw}
              >
                {s.label}
              </button>
            ))}
          </div>
          <button type="button" onClick={() => connectionErrorStore.dismiss()} style={{ ...btn, marginTop: 2 }}>
            Скрыть баннер
          </button>
        </div>
      )}
    </div>
  );
}

const btn: CSSProperties = {
  padding: '5px 8px',
  borderRadius: 6,
  border: '1px solid var(--border-2)',
  background: 'var(--bg-2)',
  color: 'var(--text-2)',
  cursor: 'pointer',
  fontSize: 10,
  fontFamily: 'inherit',
  textAlign: 'left',
};
