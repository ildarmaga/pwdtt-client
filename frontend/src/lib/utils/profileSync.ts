import type { Server } from '../types';
import { SaveProfile } from '../../../wailsjs/go/backend/App';

/**
 * Пишет Go-профиль, ключ — уникальный server.id (НЕ отображаемое имя).
 *
 * Раньше профиль хранился по name, из-за чего два сервера с одинаковым именем
 * (частый случай при импорте из одной панели) писались в один файл на диске —
 * переключение между ними ничего не меняло. Теперь ключ — id, поэтому каждый
 * сервер всегда отображается в собственный профиль.
 */
export async function saveServerProfile(s: Server): Promise<void> {
  await SaveProfile(s.id, {
    peer: s.host,
    password: s.password,
    hashes: (s.hashes ?? []).filter(h => h.trim()),
    turn: '',
    port: '',
    device_id: s.deviceId ?? '',
    listen: '',
  });
}
