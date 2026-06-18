import { useEffect, useState } from 'react';
import { IconAlertTriangle, IconRefresh, IconX } from '@tabler/icons-react';
import { connectionErrorStore } from '../lib/stores/connectionErrorStore';
import { tunnelStore } from '../lib/stores/tunnelStore';
import { toastStore } from '../lib/stores/toastStore';
import { Reconnect as WailsReconnect } from '../../wailsjs/go/backend/App';

export default function ConnectionErrorBanner() {
  const [err, setErr] = useState(connectionErrorStore.get());
  const [reconnecting, setReconnecting] = useState(false);

  useEffect(() => connectionErrorStore.subscribe(setErr), []);

  if (!err) return null;

  const degraded = err.kind === 'degraded';

  const handleReconnect = async () => {
    if (reconnecting) return;
    setReconnecting(true);
    tunnelStore.set('connecting');
    try {
      await WailsReconnect();
      connectionErrorStore.dismiss();
    } catch (e) {
      tunnelStore.set('idle');
      const msg = e instanceof Error ? e.message : String(e ?? '');
      toastStore.show(msg || 'Не удалось переподключиться', 4000);
    } finally {
      setReconnecting(false);
    }
  };

  return (
    <div className="connect-error" role="alert">
      <div className="connect-error__icon">
        <IconAlertTriangle size={18} stroke={2} />
      </div>
      <div className="connect-error__body">
        <div className="connect-error__title">
          {degraded ? 'Проблема с туннелем' : 'Не удалось подключиться'}
        </div>
        <div className="connect-error__text">{err.message}</div>
        {degraded && (
          <button
            type="button"
            className="connect-error__reconnect"
            disabled={reconnecting}
            onClick={() => void handleReconnect()}
          >
            <IconRefresh size={15} stroke={2} />
            {reconnecting ? 'Переподключение…' : 'Переподключить'}
          </button>
        )}
      </div>
      <button
        type="button"
        className="connect-error__close"
        aria-label="Закрыть"
        onClick={() => connectionErrorStore.dismiss()}
      >
        <IconX size={16} />
      </button>
    </div>
  );
}
