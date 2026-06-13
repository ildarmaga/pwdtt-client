import { useEffect, useState } from 'react';
import { IconAlertTriangle, IconX } from '@tabler/icons-react';
import { connectionErrorStore } from '../lib/stores/connectionErrorStore';

export default function ConnectionErrorBanner() {
  const [err, setErr] = useState(connectionErrorStore.get());

  useEffect(() => connectionErrorStore.subscribe(setErr), []);

  if (!err) return null;

  return (
    <div className="connect-error" role="alert">
      <div className="connect-error__icon">
        <IconAlertTriangle size={18} stroke={2} />
      </div>
      <div className="connect-error__body">
        <div className="connect-error__title">Не удалось подключиться</div>
        <div className="connect-error__text">{err.message}</div>
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
