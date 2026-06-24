import { useState, useEffect } from 'react';
import { isBrowserDev } from './dev/mockWails';
import { devWindowSizeStore } from './dev/devWindowSize';

/** Ширина «телефонного» превью (420 / 480 px). */
const MOBILE_WIDTH_MAX = 520;

function computeMobileUI(): boolean {
  if (!isBrowserDev) return false;
  return devWindowSizeStore.get().width <= MOBILE_WIDTH_MAX;
}

/** true в мобильном превью / узком окне — без трея и автозапуска. */
export function useMobileUI(): boolean {
  const [mobile, setMobile] = useState(computeMobileUI);

  useEffect(() => {
    if (!isBrowserDev) return;
    return devWindowSizeStore.subscribe(size => {
      setMobile(size.width <= MOBILE_WIDTH_MAX);
    });
  }, []);

  return mobile;
}
