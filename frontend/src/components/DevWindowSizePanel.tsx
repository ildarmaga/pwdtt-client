import { useEffect, useState, type CSSProperties } from 'react';
import { isBrowserDev } from '../lib/dev/mockWails';
import { DEFAULT_DEV_WINDOW, devWindowSizeStore } from '../lib/dev/devWindowSize';

const PRESETS: { label: string; w: number; h: number }[] = [
  { label: '650×730', w: 650, h: 730 },
  { label: '900×600', w: 900, h: 600 },
  { label: '480×720', w: 480, h: 720 },
  { label: '420×640', w: 420, h: 640 },
];

export default function DevWindowSizePanel() {
  const [open, setOpen] = useState(true);
  const [size, setSize] = useState(() => devWindowSizeStore.get());
  const [wInput, setWInput] = useState(String(size.width));
  const [hInput, setHInput] = useState(String(size.height));

  useEffect(() => devWindowSizeStore.subscribe(next => {
    setSize(next);
    setWInput(String(next.width));
    setHInput(String(next.height));
  }), []);

  if (!isBrowserDev) return null;

  const apply = () => {
    const w = parseInt(wInput, 10);
    const h = parseInt(hInput, 10);
    if (!Number.isFinite(w) || !Number.isFinite(h)) return;
    devWindowSizeStore.set(w, h);
  };

  const copySize = () => {
    const text = `${size.width}×${size.height}`;
    void navigator.clipboard?.writeText(text);
  };

  return (
    <div style={{
      position: 'fixed', left: 12, bottom: 12, zIndex: 9998,
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
        {open ? '▾ Размер окна (dev)' : '▸ Размер окна'}
      </button>
      {open && (
        <div style={{ padding: '8px 10px', display: 'flex', flexDirection: 'column', gap: 8 }}>
          <div style={{ color: 'var(--text-3)', lineHeight: 1.35 }}>
            Симуляция окна Wails. Подберите размер — потом скажете точные W×H.
          </div>
          <div style={{ display: 'flex', gap: 6, alignItems: 'center' }}>
            <label style={{ display: 'flex', flexDirection: 'column', gap: 2, flex: 1 }}>
              <span style={{ fontSize: 10, color: 'var(--text-4)' }}>Ширина</span>
              <input
                type="number"
                min={320}
                max={2400}
                value={wInput}
                onChange={e => setWInput(e.target.value)}
                onKeyDown={e => { if (e.key === 'Enter') apply(); }}
                style={inputStyle}
              />
            </label>
            <label style={{ display: 'flex', flexDirection: 'column', gap: 2, flex: 1 }}>
              <span style={{ fontSize: 10, color: 'var(--text-4)' }}>Высота</span>
              <input
                type="number"
                min={400}
                max={1600}
                value={hInput}
                onChange={e => setHInput(e.target.value)}
                onKeyDown={e => { if (e.key === 'Enter') apply(); }}
                style={inputStyle}
              />
            </label>
          </div>
          <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap' }}>
            <button type="button" onClick={apply} style={btnPrimary}>Применить</button>
            <button type="button" onClick={copySize} style={btnSecondary} title="Скопировать W×H">
              {size.width}×{size.height}
            </button>
            <button
              type="button"
              onClick={() => devWindowSizeStore.reset()}
              style={btnSecondary}
              title={`Сброс (${DEFAULT_DEV_WINDOW.width}×${DEFAULT_DEV_WINDOW.height})`}
            >
              Сброс
            </button>
          </div>
          <div style={{ display: 'flex', gap: 4, flexWrap: 'wrap' }}>
            {PRESETS.map(p => (
              <button
                key={p.label}
                type="button"
                onClick={() => devWindowSizeStore.set(p.w, p.h)}
                style={{
                  ...btnSecondary,
                  padding: '3px 6px',
                  fontSize: 10,
                  background: size.width === p.w && size.height === p.h ? 'var(--bg-3)' : 'var(--bg-2)',
                  borderColor: size.width === p.w && size.height === p.h ? 'var(--accent)' : 'var(--border-2)',
                }}
              >
                {p.label}
              </button>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}

const inputStyle: CSSProperties = {
  width: '100%',
  boxSizing: 'border-box',
  padding: '5px 6px',
  borderRadius: 6,
  border: '1px solid var(--input-border)',
  background: 'var(--input-bg)',
  color: 'var(--text)',
  fontSize: 12,
  fontFamily: 'inherit',
};

const btnPrimary: CSSProperties = {
  padding: '5px 10px',
  borderRadius: 6,
  border: 'none',
  background: 'var(--accent)',
  color: 'var(--accent-fg)',
  cursor: 'pointer',
  fontSize: 11,
  fontWeight: 600,
  fontFamily: 'inherit',
};

const btnSecondary: CSSProperties = {
  padding: '5px 8px',
  borderRadius: 6,
  border: '1px solid var(--border-2)',
  background: 'var(--bg-2)',
  color: 'var(--text-2)',
  cursor: 'pointer',
  fontSize: 11,
  fontFamily: 'inherit',
};
