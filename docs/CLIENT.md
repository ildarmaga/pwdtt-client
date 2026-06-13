# PWDTT — PC-клиент WDTT

Документ для доработки десктопного клиента под свой WDTT-сервер и панель.

**Репозиторий:** `/root/PWDTT-patched`  
**База:** форк [PWDTT](https://github.com/luminescq/PWDTT) (десктопная версия [proxy-turn-vk-android](https://github.com/amurcanov/proxy-turn-vk-android))  
**Сервер/панель:** [ildarmaga/wdtt](https://github.com/ildarmaga/wdtt) — см. `docs/SERVER.md`, релиз **v1.2.1+**

---

## 1. Назначение

PWDTT — десктопное приложение (Windows/Linux) на **Wails v2** (Go + React):

```
[UI React]  ←→  [backend Go]  ←→  [client/core Go]  ←→  VK TURN/DTLS  ←→  wdtt-server  ←→  интернет
                      ↓
              WireGuard (wg-turn / wintun)
```

С точки зрения провайдера трафик выглядит как зашифрованный VK-звонок (RTP + ChaCha20, DTLS).

---

## 2. Связь с WDTT panel

| Компонент | WDTT panel / server | PWDTT client |
|-----------|---------------------|--------------|
| Протокол туннеля | DTLS + WRAP + WG | ✅ тот же (`client/core`) |
| Пароль пользователя | `wdtt_users.password` | поле `password` в профиле |
| Адрес сервера | `ip` / домен inbound | `peer` = `host:dtlsPort` |
| VK-хеши | вручную из `vk.com/call/join/<hash>` | 1–4 хеша в настройках или профиле |
| Ссылка из панели | `wdtt://` + base64(JSON) | ⚠️ **пока не поддерживается** |

### ⚠️ Главное расхождение: формат `wdtt://` — **ИСПРАВЛЕНО в `-patched`**

**WDTT panel v1.2.1** генерирует:

```json
{"ps":"PC-ildar","ip":"devgamemaga.mooo.com","dtls":56000,"pass":"ildar123I","sub":"https://devgamemaga.mooo.com:2096/sub/abc123"}
```

Поле **`sub`** — URL подписки для метрик (трафик, срок). Нужно, когда `/sub/` заблокирован до подключения VPN: вставляете `wdtt://` с `sub` внутри, клиент сохраняет URL и после подключения запрашивает статистику.

**Поддерживается также URL подписки напрямую:**

```
https://devgamemaga.mooo.com:2096/sub/217f4ls7t6rrwoy0
```

Реализация:
- `backend/subscription.go` — HTTP fetch, парсинг `Subscription-Userinfo`
- `frontend/src/lib/utils/wdttLink.ts` — UI-обёртка
- Ctrl+V / модал «+» принимают оба формата

Старый colon-формат `wdtt://IP:56000:56001:0:pass` по-прежнему работает.

---

## 3. Структура проекта

```
PWDTT-patched/
├── main_linux.go / main_windows.go   # точка входа Wails, embed frontend + deploy
├── wails.json                        # имя приложения, сборка
├── backend/                          # мост UI ↔ core
│   ├── app.go                        # Wails API: Connect, SaveProfile, CheckVPN…
│   ├── orchestrator.go               # запуск/остановка core, логи в UI
│   ├── deploy.go                     # SSH-деплой VPS (встроенный deploy.sh + wdtt-server)
│   ├── settings.go                   # tray
│   ├── win_wg.go / wg_linux.go       # WireGuard интерфейс
│   └── win_tray.go / tray_linux.go   # системный трей
├── client/                           # ядро туннеля (module wg-turn-client)
│   └── core/
│       ├── core.go                   # Config, Core, события
│       ├── session.go                # DTLS + TURN relay
│       ├── creds.go                  # VK OAuth → TURN credentials
│       ├── creds_vkcalls.go          # ★ патч: путь через api.vk.me
│       ├── wrap.go / obfs.go         # RTP-обфускация
│       ├── wgconfig.go               # конфиг WG для локального интерфейса
│       └── captcha_*.go              # решение VK captcha
├── frontend/                         # React + Vite + TypeScript
│   └── src/
│       ├── pages/Connect.tsx         # главный экран (кнопка питания)
│       ├── pages/Logs.tsx            # логи сессии
│       ├── modals/                   # Add-server, Settings, Deploy, Hash…
│       ├── lib/utils/wdttLink.ts     # ★ парсинг wdtt://
│       └── lib/store.ts              # localStorage: серверы, настройки
└── assets/
    ├── server/deploy.sh              # скрипт установки на VPS (embed)
    ├── server/wdtt-server            # бинарник для деплоя (embed, обновлять вручную)
    └── icons/                        # иконки приложения и трея
```

---

## 4. UI — экраны и модалки

| Экран | Файл | Назначение |
|-------|------|------------|
| Подключение | `pages/Connect.tsx` | выбор сервера, кнопка «Подключить/Отключить» |
| Логи | `pages/Logs.tsx` | поток log-событий из Go |
| Sidebar | `components/Sidebar.tsx` | навигация, Deploy, Settings, тема |
| Добавить сервер | `modals/Add-server.tsx` | ссылка или IP+порт+пароль |
| Настройки | `modals/Settings.tsx` | MTU, power/workers, tray, VK-хеши |
| Deploy | `modals/Deploy.tsx` | установка wdtt-server по SSH |
| VK хеши | `modals/Hash.tsx` | 4 глобальных хеша |

**Главный экран (скриншот):** кнопка питания по центру, снизу — выпадающий список профилей («WDTT»), `+` — добавить сервер.

---

## 5. Поток подключения

1. Пользователь выбирает профиль и нажимает «Подключить».
2. `Connect.tsx` → `WailsConnect({ profile, captchaMode, workers, mtu, hashes })`.
3. `backend/orchestrator.go` загружает `~/.config/pwdtt/profiles/<name>.json`.
4. `client/core` для каждого worker:
   - по VK hash получает TURN credentials (`creds.go` / `creds_vkcalls.go`);
   - поднимает DTLS к `peer` (IP:DTLS);
   - проксирует WireGuard через TURN.
5. `backend/wg_*.go` поднимает локальный WG-интерфейс (`wg-turn`).
6. События `state_changed`, `log`, `error` → React через Wails Events.

### Обязательные данные для коннекта

- **peer** — `host:56000` (DTLS порт; WG порт клиенту не нужен в colon-формате, в JSON тоже убран)
- **password** — пароль пользователя WDTT
- **hashes** — минимум 1 VK join hash (из `https://vk.com/call/join/<hash>`)

Без хешей подключение не стартует (toast в `Connect.tsx`).

---

## 6. Хранение данных

| Данные | Где |
|--------|-----|
| Список серверов (UI) | `localStorage` ключ `wdtt_servers` |
| Настройки приложения | `localStorage` ключ `wdtt_settings` |
| Deploy-форма | `localStorage` ключ `wdtt_deploy` |
| Go-профили | `~/.config/pwdtt/profiles/<имя>.json` |
| Логи сессий | `~/.config/pwdtt/logs/<дата>_<peer>.log` |

Формат Go-профиля (`ProfileData`):

```json
{
  "peer": "example.com:56000",
  "password": "userpass",
  "hashes": [],
  "device_id": "uuid",
  "listen": "",
  "turn": "",
  "port": ""
}
```

---

## 7. Патчи в `-patched`

Отличия от upstream PWDTT, важные для WDTT:

### `client/core/creds_vkcalls.go`

Новый путь получения TURN через **`api.vk.me`** (как iOS VK Calls), без `login.vk.ru`:

1. `auth.getAnonymToken`
2. `calls.joinCallByLink`
3. → TURN username/password/urls

Вызывается **первым** в `fetchVkCreds()`; при ошибке — fallback на legacy цепочку в `creds.go`.

### Прочее

- DNS VK-хостов: `client/core/vk_dns.go`
- Store keys уже с префиксом `wdtt_*` (подготовка к ребрендингу)

---

## 8. Что менять под себя

### Брендинг (PWDTT → WDTT)

| Место | Файл |
|-------|------|
| Заголовок окна | `main_linux.go`, `main_windows.go` → `Title: "PWDTT"` |
| Имя в меню Linux | `Linux.ProgramName` |
| Wails config | `wails.json` → `"name"` |
| Иконки | `assets/icons/icon.png`, `tree-icon.png` |
| Favicon | `frontend/public/favicon.svg` |

### Поддержка ссылок из WDTT panel

**Файл:** `frontend/src/lib/utils/wdttLink.ts`

```typescript
// Псевдокод доработки
if (!stripped.includes(':')) {
  const json = JSON.parse(atob(stripped));
  return {
    ip: json.ip ?? json.add,
    dtlsPort: String(json.dtls),
    password: json.pass ?? json.id,
    name: json.ps ?? 'Server',
    hashes: parseHashes(json.hash),
  };
}
// иначе — текущий colon-парсер
```

Также обновить README.md (сейчас описан только colon-формат).

### Убрать / заменить Deploy

Встроенный деплой тянет **старый** `assets/server/wdtt-server` и `deploy.sh`.

Если сервер ставится через [wdtt-install](https://github.com/ildarmaga/wdtt-install):

- скрыть кнопку Deploy в `Sidebar.tsx` / `Layout.tsx`;
- или заменить `assets/server/*` на актуальные из релиза WDTT;
- или открывать URL панели вместо SSH-деплоя.

### Синхронизация с подпиской

Опционально: кнопка «Импорт из подписки» — HTTP GET `https://host:2096/sub/<sub_id>?format=raw`, decode base64 → wdtt link → `parseWdttUrl`.

### Workers / power

- Глобально: `settings.power` (по умолчанию 9)
- На профиль: `server.power` или `hashes.length * 9`
- Передаётся в `ConnectParams.workers` → число параллельных TURN-потоков

---

## 9. Wails API (Go → JS)

Генерируется в `frontend/wailsjs/go/backend/App.js` после `wails dev` / `wails build`.

Основные методы:

| Метод | Назначение |
|-------|------------|
| `Connect(params)` | старт туннеля |
| `Disconnect()` | остановка |
| `IsRunning()` | статус |
| `SaveProfile(name, data)` | сохранить Go-профиль |
| `GetProfile(name)` | прочитать профиль |
| `CheckVPN()` | предупреждение о других VPN |
| `SetTrayEnabled(bool)` | трей |
| Deploy-методы | SSH установка (см. `deploy.go`) |

---

## 10. Сборка

**Зависимости:** Go 1.22+, Node 18+, Wails v2, `wireguard-tools` (Linux).

```bash
cd /root/PWDTT-patched

# Linux deps
sudo apt install libayatana-appindicator3-dev pkg-config gcc wireguard-tools

# Frontend
cd frontend && npm install && cd ..

# Сборка
go install github.com/wailsapp/wails/v2/cmd/wails@latest
wails build -platform linux/amd64 -o pwdtt-linux-amd64
# → build/bin/pwdtt-linux-amd64

# Windows (кросс-компиляция)
wails build -platform windows/amd64
```

Перед релизом обновить embed-бинарник:

```bash
cp /path/to/wdtt-server-linux-amd64 assets/server/wdtt-server
```

---

## 11. Чеклист доработок под WDTT

- [x] **P0** — парсер `wdtt://` base64 JSON (`ip`, `pass`, `dtls`, `ps`)
- [x] **P0** — импорт URL подписки `https://.../sub/<id>`
- [x] **P1** — метрики сессии (TX/RX, TURN/DTLS/Internet RTT) под кнопкой
- [x] **P1** — поля `vpn` + `name` в wdtt-ссылке и UI
- [x] **P1** — объявление подписки (Announce) в интерфейсе
- [ ] **P1** — ребрендинг PWDTT → WDTT (название, иконки)
- [ ] **P1** — обновить `assets/server/wdtt-server` до v1.2.1+
- [ ] **P2** — убрать или переписать встроенный Deploy под wdtt-install
- [ ] **P3** — «Был(а) в сети» с sub-страницы

---

## 12. Отладка

```bash
# Dev-режим с hot reload UI
wails dev

# Логи ядра в UI → вкладка Logs
# Полный лог на диске:
~/.config/pwdtt/logs/

# Типичные ошибки
# - «Добавьте хеши» → Settings → VK Хеши
# - CAPTCHA_WAIT_REQUIRED → решить captcha (auto/webview)
# - «другие VPN» → отключить сторонний VPN перед коннектом
```

---

## 13. Связанные документы WDTT

| Документ | Содержание |
|----------|------------|
| `wdtt/docs/SERVER.md` | протокол wdtt-server, порты, БД |
| `wdtt/docs/API.md` | API панели, формат ссылки v1.2.1 |
| WDTT release v1.2.1 | sub-страница, `last_seen_at`, новый JSON в ссылке |

---

## 14. Changelog (2026-06-13)

### Клиент (pwdtt-client)

- **Кнопка подключения** — Soft Glow (вариант 5): зелёное свечение при активном VPN, кольцо при подключении.
- **Метрики сессии** — карточка всегда видна; при отключении значения «—», при VPN — TX/RX, скорость, TURN / DTLS / Интернет RTT (как в iOS vk-turn-proxy).
- **Заголовок сервера** — «MAGIC VPN - PC-ildar» (`vpn` + `name`).
- **Объявление подписки** — отдельная строка под блоком сервера; UTF-8 для русского `Announce`.
- **Импорт wdtt://** — поля `vpn` и `name`; исправлен dev-мок `ParseWdttLink`.
- **Настройки** — интервал обновления метрик подписки (`metricsRefreshSec`).
- **Стабильность** — TURN quota backoff, stagger воркеров (без лимита workers).
- **UI** — кнопка + метрики в одном блоке над панелью сервера; превью кнопок `/button-previews.html`.

### Панель (wdtt)

- **`vpn` в wdtt-ссылке** — из «Заголовок подписки» (`SubTitle`), не из inbound tag.
- **`name`** — комментарий пользователя; подпись на sub-странице из `json.name`.
- **JS** — `wdtt-share.js`, модал пользователя: `vpnTitle` из настроек подписки.

---

*Документ создан для внутренней доработки клиента под WDTT. Обновляйте по мере изменений в panel/server.*
