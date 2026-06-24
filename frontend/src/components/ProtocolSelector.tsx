import type { CSSProperties } from 'react';
import type { TunnelProtocol } from '../lib/types';

interface Props {
  value: TunnelProtocol;
  onChange: (p: TunnelProtocol) => void;
  locked?: boolean;
}

const PROTOCOL_META: Record<TunnelProtocol, { label: string; hint: string; accent: string }> = {
  vk: { label: 'VK', hint: 'VK Calls · TURN', accent: '#2787f5' },
  wb: { label: 'WB', hint: 'WB Stream · WebRTC', accent: '#6d6aac' },
};

export default function ProtocolSelector({ value, onChange, locked }: Props) {
  const meta = PROTOCOL_META[value];

  return (
    <div className={`protocol-bar${locked ? ' protocol-bar--locked' : ''}`}>
      <div className="protocol-seg" role="tablist" aria-label="Протокол туннеля">
        {(['vk', 'wb'] as const).map(p => {
          const m = PROTOCOL_META[p];
          const active = value === p;
          return (
            <button
              key={p}
              type="button"
              role="tab"
              aria-selected={active}
              className={`protocol-seg-btn${active ? ' protocol-seg-btn--active' : ''}`}
              style={active ? { '--proto-accent': m.accent } as CSSProperties : undefined}
              disabled={locked}
              onClick={() => onChange(p)}
            >
              <span className="protocol-seg-label">{m.label}</span>
            </button>
          );
        })}
      </div>
      <span className="protocol-hint" style={{ color: meta.accent }}>{meta.hint}</span>
    </div>
  );
}
