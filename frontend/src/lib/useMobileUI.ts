import { useState, useEffect } from 'react';
import { isBrowserDev } from './dev/mockWails';
import { devWindowSizeStore } from './dev/devWindowSize';

/** Узкое окно: мобильное превью (420 / 480 px) или маленький Wails. */
const NARROW_WIDTH_MAX = 560;

function computeNarrow(): boolean {
  if (isBrowserDev) {
    return devWindowSizeStore.get().width <= NARROW_WIDTH_MAX;
  }
  return typeof window !== 'undefined' && window.innerWidth <= NARROW_WIDTH_MAX;
}

/** true в узком окне — компактный UI, без трея и автозапуска. */
export function useMobileUI(): boolean {
  const [narrow, setNarrow] = useState(computeNarrow);

  useEffect(() => {
    if (isBrowserDev) {
      return devWindowSizeStore.subscribe(size => {
        setNarrow(size.width <= NARROW_WIDTH_MAX);
      });
    }
    const onResize = () => setNarrow(window.innerWidth <= NARROW_WIDTH_MAX);
    window.addEventListener('resize', onResize);
    return () => window.removeEventListener('resize', onResize);
  }, []);

  return narrow;
}
