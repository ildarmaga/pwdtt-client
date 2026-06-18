# Changelog — PWDTT Client (WDTT Desktop)

## [0.3.21] — 2026-06-16

### Удалено
- Вкладка **VK login** и весь связанный backend (WebView2 helper, cookies export, proxy). Остаётся только VPN.

### Добавлено
- Баннер **«Переподключить»** при падении всех воркеров (0 активных >12 с или аварийный выход core).
- Backend `Reconnect()` — stop + connect с последними параметрами.
- В логах `[RELAY] воркер #N host=… life=…` при каждом обрыве TURN-сессии.

### UI
- Toast и баннеры ошибок: перенос длинных сообщений (не обрезаются).
- Окно **680×860** — настройки и редактор сервера без прокрутки.
- **ID устройства** целиком, с переносом строки.

### Примечание
- VK **хеши** в настройках профиля и split-маршруты VK TURN (напрямую, не через туннель) — часть VPN, не удалялись.

## [0.3.20] — 2026-06-18

### Изменено
- VK login: основной вход через **WebView2 helper** (окно «WDTT — вход VK»), Edge только fallback.
- Профиль: `webview-vk/profile` — cookies export и login в одном месте.

### Исправлено
- Edge app mode открывал vk.com в чужом профиле → «время сессии истекло» и export без remixsid.

## [0.3.19] — 2026-06-18

### Исправлено
- VK login: один профиль `webview-vk/profile`, mutex — больше не плодятся 3–5 сессий `s-*` параллельно.
- VK login: убран автозапуск Edge при открытии вкладки (только кнопки «Открыть VK» / ↻).
- VK login: «сессия истекла» из‑за гонки нескольких окон Edge с разными профилями.

## [0.3.18] — 2026-06-18

### Исправлено
- VK Export Cookies: ищет `remixsid` во всех профилях `webview-vk/s-*`, не только в последнем active.
- VK login: «Открыть VK» больше не создаёт новую сессию каждый раз (только ↻/Сброс).
- VK login: определение «окно открыто» через lockfile профиля Edge, не PowerShell.

## [0.3.17] — 2026-06-18

### Исправлено
- VK login: PowerShell для проверки Edge — скрытое окно (`CREATE_NO_WINDOW`), реже опрос (5 с).
- VK login: «время сессии истекло» — каждый вход в новый каталог профиля `webview-vk/s-*`, полный kill Edge перед открытием.
- VK login: исправлен сломанный export helper (`runVKCookiesCLI`).

## [0.3.16] — 2026-06-18

### Исправлено
- VK login: «время сессии истекло» — перед открытием очищается профиль Edge (`webview-vk`); кнопка ↻ = `VKLoginRefresh`.
- VK login: Edge-лаунcher завершался сразу — wdtt отслеживает процесс Edge по профилю, не лезет export/check пока окно открыто.
- VK login: повторное «Открыть VK» не запускает второй Edge с тем же профилем.

## [0.3.15] — 2026-06-18

### Исправлено
- VK login: окно открывается через **Microsoft Edge** (`--app=https://vk.com/`) — надёжнее, чем встроенный WebView2-helper.
- VK login: helper пересобирается при обновлении wdtt (sha256), `-H windowsgui` + `native_webview2loader`.
- VK login: ошибки helper/Edge пишутся в `vk-login.log`.

## [0.3.14] — 2026-06-18

### Исправлено
- VK login: окно не открывалось — профиль WebView2 вынесен в `webview-vk` (не конфликтует с UI Wails).
- VK login: фоновая проверка cookies больше не запускает второй WebView2, пока открыто окно входа.
- VK login: ошибки запуска окна возвращаются в UI и пишутся в `vk-login.log`.

## [0.3.13] — 2026-06-18

### Изменено
- VK login: **прямой `https://vk.com/`** в отдельном окне WebView2 (как Creator), без Go-прокси.
- Export Cookies: `remixsid` из профиля WebView2 (`%APPDATA%\\pwdtt\\webview-vk`).

### Исправлено
- QR-вход и WebSocket больше не ломаются прокси-обёрткой URL.

## [0.3.12] — 2026-06-18

### Исправлено
- VK login: URL в HTML/JS теперь path-only (`/vk/login/h/...`) — больше нет вложенных `http:/127.0.0.1:...` в путях CDN.
- VK login: `rewriteVKRootPaths` не дублирует префикс `/vk/login/h/.../vk/login/h/...` в webpack publicPath.
- VK login: перехват `location.assign/replace/href` и `window.open` — после QR-входа редиректы на `vk.com`/`login.vk.com` идут через прокси и `remixsid` попадает в jar.
- VK login: GET-редиректы CDN догоняются на сервере (отдельный timeout, без `context canceled`); исправлен `?query?query`.
- VK login: кнопка ↻ — только обновление iframe; полный сброс — отдельная кнопка «Сброс».
- VK login: лог `Set-Cookie` с `login.vk.com` / `api.vk.com/auth.*` для диагностики.

## [0.3.11] — 2026-06-17

### Добавлено
- Вкладка **VK** в сайдбаре: вход в VK и **Export Cookies** — как в WhitelistBypass Creator; cookies в `~/.config/pwdtt/secrets/cookies-vk.json`.

### Изменено
- VK login: встроенный iframe в приложении (локальный прокси → `https://vk.com/`), без отдельных exe.

### Исправлено
- VK login: прокси WebSocket (QR-вход через `id.vk.com`) + исправлен JS-хук для `vk.com` (не только поддоменов).
- VK login: исправлена двойная подмена URL (`http:/127.0.0.1:.../vk/login/h/...` → 404).
- VK login: iframe `id.vk.com` перехватывается JS-хуком (раньше обходил прокси).
- VK login: лог в `%APPDATA%\\pwdtt\\logs\\vk-login.log` и рядом с `wdtt.exe`; хвост лога на вкладке VK.
- VK login: автосохранение cookies при появлении `remixsid` в прокси-jar (`VKLoginSync`).
- Старт приложения: убран импорт `go-webview2/edge` из backend (ломал Wails через `LockOSThread` в `init`).
- VK login: `gzip: invalid header` — без повторной gzip-распаковки, `Accept-Encoding: identity`.
- VK login: исправлен прокси (путь `/vk/login/`, старт `https://vk.com/`).

## [0.3.10] — 2026-06-17

### Добавлено
- Импорт **wdtt:// с полем `sub`** — клиент извлекает URL подписки и загружает профиль с панели.

### Изменено
- Валидация sub URL: любой путь и порт из настроек панели (не только `/subs/` и `:2096`).

## [0.3.9] — 2026-06-17

### Добавлено
- Импорт **только** по ссылке подписки WDTT-панели (`https://…/subs/…` или `/sub/…`).
- Валидация URL подписки на Go и в UI — отклоняются `wdtt://` и произвольные ссылки.
- Поддержка `did` / `device_id` из ответа подписки (привязка устройства из панели).
- Парсер JSON: поле `vk_hash` (раньше только `hash`).

### Изменено
- «Добавить сервер» — одно поле: URL подписки из панели; ручной ввод IP/пароля убран.
- Paste (Ctrl+V) — только ссылка подписки панели.
- `ParseWdttLink` отключён для прямого импорта `wdtt://`.

### Исправлено
- Сломанный `postbuildcommand` в `wails.json` (копирование несуществующего `go_client`).

## [0.3.8] — 2026-06-14

- Активный профиль в UI при подключённом VPN.
