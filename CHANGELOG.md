
# Changelog — PWDTT Client (WDTT Desktop)

## [0.3.225] — 2026-07-21

### Fix — soft-rebind без ложного полного reconnect
- После RestartLink старые SOCKS-dial’ы сыпали `closed pipe` → клиент сразу делал полный reconnect.
- 12s ignore OpenStream после rebind; dial-hold на joiner; при сбое — ещё soft WebRTC-сессия, полный reconnect только после исчерпания soft.
- Сервер ≥1.4.84.

## [0.3.224] — 2026-07-21

### Fix — восстановление WB после смены сети (SOCKS / v2rayN)
- Soft-rebind больше не зависает на 0 B/s: creator+joiner синхронно rebind’ят KCP/smux; убран двойной SwapTunnel.
- SOCKS: короткий grace (20s) + verify по трафику; лавина `OpenStream closed pipe` → полное переподключение (не вечный soft).
- Нужен сервер ≥1.4.83.

## [0.3.223] — 2026-07-10

### UI — SOCKS URL как на iOS
- Копирование: `socks://BASE64(user:pass)@127.0.0.1:10809#MAGIC_VPN-ildar` (формат Happ / iOS `v2rayProxyUri`), не plaintext `user:pass@`.

## [0.3.222] — 2026-07-10

### Fix — SOCKS UDP для Steam / v2rayN TUN
- UDP ASSOCIATE стал двусторонним: unsolicited datagrams (Steam SDR) больше не теряются.
- Нужен сервер ≥1.4.82. Тест `TestSocksUDPUnsolicitedDatagrams` зелёный до релиза.

## [0.3.221] — 2026-07-10

### Fix — dual-track: drain #3+#4, без seq/reorder, тесты до релиза
- ICE rebind давал track #3/#4 в тот же VP8 → `remote not ready`; scale-up RestartLink только 1 раз.
- KCP = camera-only без seq (как single). Сервер ≥1.4.81.
- Регрессии прогнаны: SOCKS×48 dual, drain policy, no-reorder.

## [0.3.220] — 2026-07-10

### Fix — dual-track: KCP только на camera
- v0.3.219 redundant fan-out удваивал uplink и не чинил creator (сервер ещё на striping).
- Теперь `SendRaw` всегда на camera; screenshare только для SFU. Нужен сервер ≥1.4.80.

## [0.3.219] — 2026-07-10

### UI — SOCKS overlay на весь экран
- Модалка SOCKS рендерилась внутри `.connect-center` (`transform`) → `position:fixed` только по центру (зелёное пятно). Теперь portal в `document.body` + плотное затемнение без `backdrop-filter` (WebView2 ломает blur).

### Fix — dual-track WBT
- `SendRaw` больше не чередует треки (striping): один и тот же seq уходит на camera+screenshare. SFU часто роняет screenshare → дыры в reorder → `remote not ready` / долгие SOCKS dial в Telegram. Первый пришедший кадр выигрывает, дубликат отбрасывается.

## [0.3.218] — 2026-07-10

### UI — формат SOCKS URL
- Копирование: `socks://user:pass@127.0.0.1:10809#MAGIC_VPN-ildar` (vpn + имя профиля).

## [0.3.217] — 2026-07-10

### UI — Telegram открывает прокси сам
- Кнопка **Открыть в Telegram** → `tg://socks?server=&port=&user=&pass=` через BrowserOpenURL.

## [0.3.216] — 2026-07-10

### UI — компактные настройки + SOCKS как модалка
- Настройки: меньше шрифты, степперы, тумблеры и отступы.
- SOCKS: маленькая кнопка в шапке статистики → модалка в формате настроек (URL / адрес / Telegram).

## [0.3.215] — 2026-07-10

### UI — SOCKS сбоку у статистики
- Кнопка **SOCKS** справа от карточки сессии; панель по клику (не в настройках).
- В панели: адрес / auth, Копировать URL, Адрес, **Telegram** (ссылка `t.me/socks` в буфер).

### Fix — стоп/рестарт и «Подключить» при живом туннеле
- `Joiner.Close` закрывал SOCKS-потоки только после drain → до 25 с «Ожидание…» / force stop. Теперь клиентские коннекты рвутся сразу (timeout 2 с).
- Shutdown wait 25→8 с; повторный Connect при живом туннеле синхронизирует UI вместо ошибки «уже запущен».
- Главный экран при монтировании сверяет `IsWBRunning` / SOCKS endpoint с бэкендом.
## [0.3.214] — 2026-07-10

### UI — SOCKS компактнее
- Убраны длинные подсказки про v2rayN / Dual-track из настроек.
- «Показывать логи» убрано — логи всегда видны.
- Карточка: заголовок **SOCKS5** (кнопка → настройки), без «для v2rayN».

### Perf — быстрее SOCKS после ICE
- Вместо жёстких 5 с ожидания: ждём carrier rebound (обычно ~1 с) + 800 мс, иначе fallback 5 с.

### Logs — подробный таймлайн всего туннеля
- Session: start → auth → signaling Start → end (с длительностями).
- Joiner: NewJoiner / RestartLink / SwapTunnel с ms; SOCKS dial #1–12 + медленные (>800 мс).
- ICE: RTP settle, dual wait, pre-SOCKS settle, RestartLink, total bring-up.

## [0.3.213] — 2026-07-10

### Fix — Dual-track 1|2 оба рабочих (с сервером 1.4.79)
- Пересборка с wbstream: joiner ждёт creator tracks=2 при Dual=on; Dual=off — 1 трек.
- Тумблер Dual-track снова выбирает режим. Нужен сервер ≥1.4.79.

## [0.3.212] — 2026-07-10

### Fix — Dual-track снова выбор пользователя
- Убран force dual на SocksOnly. Dual=off → 1 трек; Dual=on → 2 (creator scale-up).
- Нужен сервер ≥1.4.79. Сверхседено 0.3.213.

## [0.3.211] — 2026-07-10

### Fix — SocksOnly всегда dual (match creator WBT)
- Creator шлёт 2 VP8 + seq prefix; dual=off на joiner без reorder → `remote not ready` / SOCKS мёртв.
- SocksOnly форсит dualTrack=true. UI-тумблер для SOCKS-режима больше не отключает второй трек.
- Нужен сервер ≥1.4.77 (WBT tracks=2). Сверхседено 0.3.212.

## [0.3.210] — 2026-07-10

### Fix — возврат WBT/KCP для SOCKS (как до xray / gVisor)
- **Корень ERR_SSL / мёртвый ↓**: с 0.3.206 SocksOnly шёл через RelayBridge без KCP — TLS ломался (`ERR_SSL_BAD_RECORD_MAC_ALERT`), dual-track mid-session scale-up не работал.
- Снова **WBT = KCP+smux + локальный SOCKS** для v2rayN (тот же транспорт, что у рабочего gVisor-стека). Dual-track снова шардит через `SendRaw`.
- Нужен сервер ≥1.4.77 (`UseWBT=true`, tracks=2). Сверхседено 0.3.211.

## [0.3.209] — 2026-07-10

### Fix — тихий лог при нормальном закрытии SOCKS (Windows)
- Relay `IsBenignConnError` + UI `classifyWBLog`: больше не красим в `[ERROR]` обычные closes (`use of closed`, `wsarecv` / `forcibly closed by the remote host`).
- Это был шум от приложений/CDN, не падение туннеля. Нужен сервер ≥1.4.74 (тот же фильтр на creator).

## [0.3.208] — 2026-07-09

### Fix — Dual-track ломал SOCKS + переключатель в настройках
- **Корень**: `MultiTrackTunnel.SendData` шардил кадры по `connID%2` на screenshare; WB SFU часто роняет/задерживает 2-й трек → ClientHello не доходит (`EOF with no data read`), ответы приходят как `unknown conn`.
- **Фикс**: RelayBridge `SendData` всегда на camera track (как стабильный путь); `SendRaw` (WBT) по-прежнему round-robin.
- **UI**: Настройки → WB → **Dual-track** (вкл/выкл). Дефолт **выкл** (как kulikov0 `--dual-track=false`). Connect передаёт `wbDualTrack`, не хардкод `true`.
- Нужен сервер ≥1.4.69 (тот же SendData fix на creator).

## [0.3.207] — 2026-07-09

### Fix — RelayBridge: не убивать SOCKS на ICE rebind + watchdog без RTT
- **Rebind**: `SwapTunnelKeepConns(false)` при sub-offer / sub-ICE — живые SOCKS-сессии v2rayN не сбрасываются (`closeAll` больше не рвёт CONNECT mid-handshake → меньше `unknown conn` / NACK).
- **Watchdog**: `SOCKS_READY` и трафик помечают `lastHealthy` (у RelayBridge нет KCP RTT, `WBT 0 ms` — норма). Больше нет ложного «туннель не поднялся» через ~90 с при живом SOCKS.
- Soft KCP-recover в SOCKS-only отключён (бессмысленен для RelayBridge).

## [0.3.206] — 2026-07-09

### Fix — оригинальный WB (RelayBridge) + без xray + апдейтер direct
- **WB**: вместо KCP/smux — [kulikov0 RelayBridge](https://github.com/kulikov0/whitelist-bypass) (как desktop-joiner). Нужен сервер ≥1.4.68.
- **Размер**: убран embed xray.exe/geoip/geosite (~50+ МБ) из Windows-сборки; CI больше не качает xray.
- **Обновления**: `Proxy: nil` (игнор системного/v2rayN прокси) + seed LAN gateway при VK connect — скачивание с GitHub идёт напрямую.

## [0.3.205] — 2026-07-09

### Fix — простой WB SOCKS: без streamSem / per-host / AIMD wnd=64
- Эталон [kulikov0/whitelist-bypass](https://github.com/kulikov0/whitelist-bypass): `RelayBridge` без лимитов потоков. У нас KCP+smux (сервер), но лишние ограничения убивали SOCKS.
- **Убрано**: `streamSem` 128, per-host 12, delay-based AIMD (фиксированное окно 2048, без shrink до 64).
- **Фикс UI**: не слать `SOCKS_READY` на carrier rebind до реального `Listen` (ложный «туннель активен» на 5 с раньше SOCKS).
- Нужен сервер ≥1.4.67 (тот же fixed-window KCP на creator).

## [0.3.204] — 2026-07-09

### Fix — WB только SOCKS + ICE settle (как у TUN)
- **Встроенный TUN/маршрутизация WB убраны** из UI и Connect: только SOCKS для v2rayN (как iOS → V2BOX).
- **Корень нестабильности**: SOCKS поднимался сразу после WebRTC, а ICE sub-offer через мгновение делал `RestartLink` и убивал smux под живым v2rayN → `remote not ready`, WBT в секунды, `wnd=64`. У TUN уже был settle 5 с + sync — у SOCKS не было.
- **Фикс**: перед `ServeSOCKS` — 5 с ICE settle + `RestartLink` (как `bringUpVPN`). Порт по умолчанию **10809** (не пересекаться с inbound v2rayN на 10808).

## [0.3.203] — 2026-07-09

### Feature — WB SOCKS-only как iOS (для v2rayN)
- Режим **SOCKS** (по умолчанию): WDTT поднимает WebRTC/KCP и локальный SOCKS5 `127.0.0.1:10808` — без встроенного wintun/xray. Системный VPN делает **v2rayN** (как V2BOX на iOS).
- Режим **TUN** — прежний полный VPN в приложении.
- Настройки: SOCKS/TUN, порт; после подключения на главном экране — адрес + копирование `socks5://…` URL.
- Auth: авто (случайный логин/пароль) или вручную (как раньше в Proxy).

## [0.3.202] — 2026-07-09

### Fix — WB TUN: zombie streamSem после dial fail (Telegram заполнял 512 слотов)
- **Поле 0.3.201**: CC уже отпускал `wnd` (384→64→345), QUIC block (`rules=7`), лимит SOCKS срабатывал — но слоты не освобождались: creator при `dial failed` **не слал ack**, joiner ждал **90 с** → `SOCKS stream limit` спам, warmup timeout, ↓=0.
- **Рабочий эталон**: gVisor netstack (~30 МБ/с) без SOCKS-петли; здесь чиним admission на xray-пути.
- **Фикс**: creator `0x01` failure ack; connect wait 90→25 с; global cap 512→128; per-host ≤12. Нужен сервер ≥1.4.66.

## [0.3.201] — 2026-07-09

### Fix — WB TUN: после warmup окно KCP залипало (Telegram-шторм + баг CC)
- **Разбор 0.3.200**: bring-up/warmup OK (`ip=178…`), LAN bypass до split. Через ~15 с: `rtt=1435 ewma=250 floor=155 wnd=64` → пила `0 B/s`. На сервере за минуту **221 connect**, из них **166 → 149.154 (Telegram)**.
- **Причины**:
  1. `nextKCPWnd` проверял shrink по ewma **раньше** grow по recent-min → при `ewma=250 > shrinkThresh` grow недостижим, `wnd=64` навсегда (тест 1.4.63 покрывал только dead-band).
  2. `handleSOCKS` (путь xray) открывал smux **без** `streamSem` → сотни параллельных потоков в одну KCP.
  3. `mode=custom` без правил = 6 rules без блока QUIC (в global он был).
- **Фикс**: grow до shrink; `streamSem` на SOCKS; всегда block UDP/443. Нужен и клиент, и сервер ≥1.4.65 (общий relay).

## [0.3.200] — 2026-07-09

### Fix — WB TUN: LAN bypass ставился после split-default (~10 с hairpin)
- **Поле 0.3.199**: bypass появился (`LAN bypass 192.168…`, `172.31.255.254`), но **после** `split default routes` с паузой ~10 с (`20:46:00` → `20:46:10`). В это окно весь LAN/шум уже шёл в KCP → warmup timeout, `wnd=64`, пила `0 B/s`.
- **Причина**: `route -p ADD` (persistent) на Windows тормозит по секунде на маршрут; плюс порядок «сначала split, потом bypass».
- **Фикс**: LAN/sink bypass **до** split-default (RouteShell + gVisor); `route ADD` без `-p` (session-only, teardown и так чистит).

## [0.3.199] — 2026-07-09

### Fix — WB встроенный TUN: LAN/wintun-шум валился в KCP (iOS ок, ПК вставал)
- **Причина**: при переходе на xray-owned wintun (`RouteShell`) потеряли OS-bypass, который был в gVisor-пути (`desktoptun_windows.go`): RFC1918 LAN + sink `172.31.255.254`. В `global`/`custom` xray тоже не держал LAN на `direct` — весь LAN-скан/адаптерный шум шёл `wintun → xray → SOCKS → smux/KCP` и забивал несущую. На iOS V2BOX обычно режет LAN сам; на ПК после warmup (`ip=…`) трафик садился в `0 B/s`, `wnd=64`, WBT секунды.
- **Фикс**: `RouteShell.FinishTunSetup` ставит те же LAN CIDR + `172.31.255.254` bypass (metric 1), что gVisor; teardown чистит CIDR. В `wbxray` LAN/sink/`10.99.0.0/24` всегда `direct` до catch-all (defense in depth для global/custom).
- Тесты: `TestBuildConfigJSON_GlobalAlwaysLANDirect`; `./wbxray` + `./desktoptun` зелёные.

## [0.3.198] — 2026-07-09

### Fix — WB: окно KCP залипало на дне после upload-всплеска → «плавающая» загрузка
- **Инструментация 0.3.197 нашла настоящую причину** (и опровергла гипотезу про `WriteSample`): в логах **ноль** строк `SLOW WriteSample` даже в стопор — send-путь не блокируется; несущая по `selected ICE pair = udp/host→udp/srflx` (UDP, не TCP-TURN). Реальный баг виден по паре клиент+сервер: после всплеска RTT на upload (`rtt=1232ms`) окно падает на дно `wnd=64` и **зависает там 1–2 минуты**, хотя RTT уже вернулся (`rtt=44ms wnd=64` спустя 90с). Отсюда загрузка зажата в пилу: 64 сегмента / RTT = ~430 KB/s@200ms, ~2 MB/s@44ms → «резко 11, резко 6, резко 4».
- **Причина**: рост окна был привязан к медленному сглаженному `ewma` (`*0.88+*0.12`) — после всплеска до 1232 ms он декеит ниже порога роста ~10–20 тиков, а любой микро-всплеск сбрасывает назад. Окно не успевало восстановиться.
- **Фикс (Vegas/BBR-style)**: решение о **росте** окна теперь по **быстрому recent-min RTT** (минимум за 4 тика = 2с), а не по медленному ewma. «Путь свободен» определяется по минимальному RTT — окно поднимается со дна за 1–2 тика после реального спада. Shrink остаётся на сглаженном ewma + гистерезис (защита от джиттера TURN).
- Тесты: `TestNextKCPWndFastRecovery` — окно растёт со дна при elevated-ewma но низком fast-RTT (без фикса стоит на 64); все прежние CC-тесты (shrink/hysteresis/drain/floor) зелёные; bidir load-harness без регрессий.

## [0.3.197] — 2026-07-09

### Observability — где именно встаёт WB на upload (транспорт под KCP, а не KCP)
- **Разбор по слоям (реальные bidir-тесты + анализ pion-транспорта)**: при upload-всплеске WBT RTT улетает в 15c+ при ~77 KB данных в полёте — столько байт не могут создать 15c задержки, значит bloat **ниже KCP**, в pion/SRTP/ICE/TURN. `track.WriteSample` **полностью синхронный** и держит `writeMu` до возврата всего стека вниз (SRTP→DTLS→ICE→kernel/TURN send buffer); один медленный `WriteSample` замораживает **весь** VP8-писатель дорожки — и keepalive, и каждый KCP-кадр (ACK в т.ч.). Окно KCP тут бессильно.
- **Лог `vp8tunnel: SLOW WriteSample <ms>`** (порог 150 ms, rate-limit 500 ms) — прямо показывает залипание несущей и его длительность.
- **Лог `[lk] pub/sub selected ICE pair: local=… remote=…`** — какой транспорт реально несёт медиа (UDP srflx vs TCP/UDP TURN relay). Нужно, чтобы отличить залипание TCP-TURN от P2P-UDP.
- Только наблюдаемость: конфиг ICE/транспорта не менялся, поведение то же, что в 0.3.196. Сними спидтест ↑/↓ и пришли лог со строками `SLOW WriteSample` и `selected ICE pair` — по ним выберем точечный фикс (форс UDP-relay / bound TCP-буфера / детектор стопора → ребайнд).
- Плюс постоянный regression-harness `carrier_load_test.go`: секундные bidir-прогоны через смоделированную несущую (constrained uplink + deep TURN-буфер), ловит тотальный дедлок обеих сторон.

## [0.3.196] — 2026-07-08

### Fix — WB встаёт намертво на upload-всплеске (блокирующий KCP-tunnel → дедлок отдачи)
- **Причина (разбор исходников kcp-go по слоям)**: kcp-go гонит **всю** отдачу — и данные, и ACK — через **одну** горутину `postProcess` → `defaultTx`, которая по очереди зовёт наш `kcpConn.WriteTo`. Приоритетная ACK-полоса (0.3.194) стоит **после** этой сериализации, поэтому не спасала. Наш `WriteTo` при полном `outbound` **блокировался бесконечно** (нет write-дедлайна) → `postProcess` замирал → вставала **вся** отдача, включая ACK → RTO-экспонента (RTT 1152→3128→21671 ms), `OpenStream: timeout`, ↓/↑ = 0 без восстановления. Крошечный upload (30 KB/s) ронял оба направления — это дедлок, не насыщение.
- **`kcpConn` теперь неблокирующий**, как настоящий UDP-сокет: `WriteTo` и `deliver` дропают при полной очереди вместо блокировки. KCP надёжный — потерянный сегмент ретрансмитится по RTO; зато tx/rx-горутины несущей больше никогда не замирают. Убрана и старая 2-сек блокировка на приёме (стопорила весь read-путь несущей).
- **Потолок окна 1024 → 384**: на быстром low-RTT пути окно доgrowало до 1024 (≈1.2 MB in-flight), хотя 2 MB/s при 70 ms нужно ~120 сегментов. Этот лишний 1.2 MB был «боезапасом»: upload-всплеск вываливал его в lossy WB/TURN разом → RTT в потолок. 384 (~460 KB) держит ~3 MB/s@150ms и режет разовый выброс.
- Тесты: `WriteTo`/`deliver` никогда не блокируют (со старым кодом виснут по таймауту), дроп рапортует успех (KCP сам ресендит), гистерезис/drain под новый потолок.

## [0.3.195] — 2026-07-08

### Fix — «плавающая» скорость скачивания на WB (AIMD-пила на джиттере TURN)
- **relay/wbtunnel**: скачивание дёргалось (↓ 1.5 MB/s → 14 KB/s → назад) при низком RTT — не затор. Dual-track VP8 идёт через lossy/jittery TURN, и один тик раздутого RTT (джиттер несущей) вызывал жёсткий cut окна → быстрый поток обваливался в пилу. VK одноканальный, без reorder — потому плавно.
- **Гистерезис на shrink**: первый «высокий» тик только слегка поджимает окно (×0.90); жёсткий пропорциональный cut — лишь если RTT высок 2 тика подряд (реальный bufferbloat). Джиттер поглощается, скорость держится; настоящий затор сливается за ~3 тика.
- Идёт вместе с приоритетной ACK-полосой (0.3.194): проверь на спидтесте одновременные ↑/↓ — раньше ↓ вставало в 0.
- Тесты: `nextKCPWnd` гистерезис (транзиент → мягко, sustained → жёстко), drain-bufferbloat (≤4 тика).

## [0.3.194] — 2026-07-08

### Fix — download встаёт в 0 при upload'е даже с ужатым KCP-окном (ACK head-of-line blocking)
- **relay/wbtunnel**: разбор по слоям показал, что стопор — не в окне (оно корректно ужималось до min). ACK'и скачивания и bulk-данные отдачи делили **один FIFO `outbound`**. При upload-всплеске очередь забивалась крупными PUSH-сегментами (~1200 Б), ACK'и приёма вставали в её хвост (до 4096 пакетов) → сервер не получал ACK вовремя → окно сервера вставало → ↓ = 0 B/s, RTT 7 c, smux рвался `OpenStream: timeout`.
- **Почему рилсы (чистое скачивание) работали**: `outbound` не забивается bulk-отдачей, ACK'и уходят мгновенно.
- **Двухполосный `outbound`**: ACK/control (KCP-пакеты без data-bearing PUSH) → приоритетная полоса, отправляется раньше любого bulk; `pumpOutbound` перед каждым тиком полностью сливает hi-полосу. Классификация — разбором KCP-заголовка (устойчиво к коалесцированным ACK), не по размеру.
- Тесты: `kcpPacketIsControl` (ACK/пробы/mixed/coalesced), `pumpOutbound` приоритет (падает без фикса — bulk обгоняет ACK); устранена гонка `l.outbound/stopCh` (каналы передаются в горутину параметрами).

## [0.3.193] — 2026-07-08

### Fix — download встаёт при одновременном upload'е (KCP window collapse latency)
- **relay/wbtunnel**: на быстром скачивании окно росло до 1024 (RTT ~66 мс). Внезапный upload-всплеск этим окном вываливал ~1.2 МБ в несущую; старый shrink ×0.7/тик драйнил буфер ~8 c — за это время WBT RTT уходил 66 → 800–2600 мс, ↓ падало в 0 B/s, новые smux-стримы отваливались (`write connect: timeout`).
- **Пропорциональный shrink**: factor = floor/rttEwma, кламп [0.25, 0.80] — чем сильнее раздут RTT, тем жёстче режем окно (1024→64 за 1–2 тика вместо ~8).
- **tuneLoop 1с→500мс** — всплеск ловится и гасится вдвое быстрее.
- Тесты: `nextKCPWnd` (пропорциональный/severe-clamp), drain-bufferbloat (≤3 тика до min).

## [0.3.192] — 2026-07-08

### Fix — трафик встаёт под upload-нагрузкой (KCP bufferbloat runaway)
- **relay/wbtunnel**: congestion control переписан с `nc`-тумблера на **delay-based AIMD размера окна** (Vegas-style). `nc=1` постоянно; окно растёт аддитивно у пола RTT и ужимается мультипликативно (×0.7) при раздувании RTT — in-flight отслеживает реальный BDP и сливает буфер TURN до убегания.
- **Симптом (лог 0.3.191)**: скачивание разгонялось до ~1.1 MB/s (RTT 56–90 мс), но как только шёл upload — старое фиксированное окно раздувало буфер, WBT RTT убегал 316 → 534 → … → 11669 мс, ↓ падало в 0 B/s, WebRTC отваливался по dtls timeout и переподключался. Формат лога `cc=off/on` = старая nc-сборка.
- **RTT floor заморожен во время затора** — floor больше не ползёт вверх, пока RTT раздут (в логе было 91 → 1609 → 2795 мс), поэтому порог shrink остаётся низким и окно продолжает ужиматься.
- Тесты: `nextKCPWnd`, `updateRTTFloor` (включая drain-bufferbloat и freeze-under-congestion).

## [0.3.191] — 2026-07-07

### Fix — медленный/пилообразный трафик на lossy TURN (adaptive KCP cc)
- **relay/wbtunnel**: delay-based congestion control — `nc=1` (nocwnd) когда RTT у пола (путь свободен, случайные потери TURN не схлопывают окно); `nc=0` когда RTT раздувается (реальная очередь).
- RTT floor с мгновенным snap-down и медленным creep-up; tuneLoop 1 с (быстрее реакция на dip).
- Тесты: `kcp_cc_test.go` — `kcpShouldDisableCC`, `updateRTTFloor`.

## [0.3.190] — 2026-07-07

### Fix — регрессия 0.3.189: с dualTrack=false трафик не шёл вообще
- Сервер-creator **всегда** публикует 2 VP8-трека и шардит KCP по ним (4-байтный seq-префикс, `wbcreator: ScreenShare: true`). Joiner включает дефрейминг/reorder только когда сам публикует >1 трека (`SubTunnelCount()>1`), т.е. при `dualTrack=true`. При `dualTrack=false` joiner не может разобрать sharded-загрузку, а creator — распарсить upload без префикса → **трафик встаёт в обе стороны**.
- 0.3.189 насильно ставила `dualTrack=false` → убивала туннель. Теперь dual-track **обязателен**: жёстко передаётся `true` при подключении (`Connect.tsx`), тумблер убран из настроек (был footgun), дефолт и миграция (`wbVp8Rev=3`) чинят сохранённое `false`.
- Пейсинг нормализуется на 30/64.

> Примечание: dual-track сейчас обязателен из-за серверной архитектуры. Ускорение (симметричный single-track или адаптивный KCP против случайных потерь) — отдельная серверная задача, будет отдельно.

## [0.3.189] — 2026-07-07

### Fix — медленный трафик (sawtooth 0↔100 KB/s): сброс legacy VP8-настроек не срабатывал
- **Баг миграции из 0.3.188**: проверка `merged.wbVp8Rev !== DEFAULT` всегда была ложной (в `merged` уже подставлена дефолтная ревизия из `DEFAULT_SETTINGS`), поэтому legacy `dualTrack=true` / `60/128` НЕ сбрасывались. Теперь сравнивается **сохранённая** ревизия (`saved.wbVp8Rev`).
- Почему это тормозило: при `dualTrack` KCP-несущая раскидывается round-robin по 2 VP8-трекам (`MultiTrackTunnel.SendRaw`). На lossy TURN треки приходят вразнобой → KCP видит переупорядочивание как потери → congestion window (`nc=0`) постоянно схлопывается → пила 108→0→108 KB/s, upload голодает. Один трек убирает reorder на приём и передачу.
- После обновления настройки принудительно станут **30/64, dual-track выкл** (однократно; можно вернуть вручную). Соединение при этом стабильно — фиксы шторма из 0.3.188 держатся.

## [0.3.188] — 2026-07-07

### Fix — обновление не скачивается при активном VPN
- **DirectBypassHosts** больше не берёт шлюз из текущего default route (при VPN это TUN `10.99.0.1`). Используется LAN-шлюз, сохранённый до поднятия туннеля (`RouteShell.Prepare` / `desktoptun.Start`), с fallback на non-TUN default route.
- Скачивание exe с GitHub при подключённом VPN снова идёт мимо туннеля.

### Fix — шторм реконнектов (smux desync `ack=0x7b`, «egress still direct»)
- **awaitShutdown generation-scoped**: аварийный стоп «зависшего» старого рана больше не убивает свежеподнятый туннель (в логе: «принудительная остановка» через 6 с после успешного warmup). Watcher захватывает done/gen своего рана и не трогает новый.
- **Run-exit handler не плодит реконнекты**: при отменённом ctx (Disconnect / reconnect / быстрый повторный Connect) выходящий ран больше не запускает параллельный dial поверх пользовательского.
- **Авторетрай реконнекта** уступает, если пользовательский Connect уже поднял новый ран (`cancel != nil`).
- **wbjrunner: setupGate** — teardown ждёт завершения bring-up xray (до 8 с), чтобы не снимать адаптер посреди запуска (оставался живой xray + split-маршруты для следующего рана).
- **Одноразовый сброс legacy VP8-настроек** (`wbVp8Rev=2`): сохранённые `dualTrack=true` / 60/128 со старых версий сбрасываются на 30/64 dual-off — асимметрия с creator (30/64 dual-off) перегружала TURN и рвала туннель.
- Диагностика: `remote not ready: ack=[123] err=%!w(<nil>)` → `smux desync ack=0x7b` (joiner прочитал байт JSON-запроса вместо ack — рассинхрон, а не занятый сервер).

## [0.3.187] — 2026-07-07

### Fix — WB медленный/нестабильный трафик на TURN (speedtest timeout)
- **Клиент v0.3.187** включает все relay-фиксы (v0.3.184–186): reorder gap flush, creator smux resync, KCP congestion control + bounded send window (`snd=1024`). На v0.3.184 joiner работал со старым KCP (`snd=2048`, без nc) — asymmetry с сервером.
- **Pacing по умолчанию 30/64**, dual-track **выкл** — 60/128 + dualTrack перегружали lossy TURN-носитель (RTT 300–800 ms, провалы 0 B/s). Creator (сервер) тоже на 30/64.
- **Настройки VP8**: поля FPS/Batch редактируемые (ввод с клавиатуры), сохранение сразу; убрана автомиграция 30/64→60/128.

## [0.3.186] — 2026-07-07

### Fix — WB bufferbloat: RTT растёт до 2–4 с и скорость падает в 0
- **KCP (обе стороны)**: включён congestion control (`nc=0`) — при потерях окно откатывается, а не долбит ретрансмитами в уже раздутый буфер носителя.
- Send-окно больше **не растёт** с RTT (был положительный feedback: RTT↑ → окно↑ → in-flight↑ → RTT↑ → коллапс). Ограничено `kcpSndWndCap=1024` (~1 BDP, ~60 Мбит/с при 160 ms) — bufferbloat ограничен, реальная скорость VPN не страдает. Receive-окно по-прежнему масштабируется (только буфер переупорядочивания).
- Проявлялось на lossy/TURN-путях (base RTT ~160 ms): `WBT 334→487→1076→4405 ms`, `↓ 0 B/s`.

## [0.3.185] — 2026-07-07

### Fix — WB туннель мёртв после рестарта сервера (`closed pipe`, KCP rtt=200ms)
- **Creator (сервер)**: `joinerNeedsKCPRestart` теперь инициализируется `true`. После рестарта сервера комната возобновлялась с `activeCreator`, но флаг был `false` — первый пришедший joiner (свежий smux-клиент) не вызывал `RestartLink`, creator держал старый smux-сервер → `OpenStream: read/write on closed pipe`, KCP голодал на `rtt=200ms` бесконечно. Теперь первый joiner после любого (ре)старта всегда пересинхронизирует smux; `OnTunnelLost` перевзводит флаг для последующих reconnect.

## [0.3.184] — 2026-07-07

### Fix — WB медленная/рваная загрузка (провалы `↓ 0 B/s` по 3–4 с)
- **Reorder dual-track**: убрано head-of-line зависание. Потерянный кадр-носитель VP8 не ретрансмитится на уровне носителя — раньше буфер держал весь downlink, пока не набьётся 128 кадров (многосекундный стоп). Теперь пробел флашится через `frameHoldTime=40 мс` и KCP (поверх) сам восстанавливает потерянный чанк. Помогает обоим направлениям (creator и joiner — приёмная сторона).
- **Клиент**: миграция старых сохранённых настроек pacing `30/64` → актуальные `60/128` (совпадает с сервером-creator, uplink не тормозит ACK-обратку). Значения, выбранные вручную, не трогаются.

## [0.3.183] — 2026-07-07

### Fix — WB download 0 B при dualTrack (главная причина «сайты не грузятся»)
- Reorder-буфер dual-track синхронизируется по первому кадру (отправитель не сбрасывает счётчик seq при KCP restart).
- Ресинхрон при большом скачке seq (перезапуск счётчика отправителя) вместо застревания всех кадров в pending.
- Устраняет `↓ 0 B` / warmup ipify timeout при активном dual-track.

## [0.3.182] — 2026-07-06

### Fix — WB smux desync (трафик не идёт после connect)
- **Creator** больше не делает KCP-restart на ICE sub-offer (только joiner) — устраняет рассинхрон smux.
- **Joiner**: отложенный rebind если sub-offer пришёл до старта KCP; sync KCP+smux перед поднятием VPN.

## [0.3.181] — 2026-07-06

### Fix — WB трафик не идёт после connect (KCP restart storm)
- **Joiner**: xray/VPN поднимается через 5 с после WebRTC — после типичного ICE sub-offer rebind.
- **Creator (сервер)**: убран лишний `RestartLink` после `SwapTunnel`; debounce peer-epoch restart 3 с.
- Устраняет `remote not ready` / warmup ipify timeout при connect.

## [0.3.180] — 2026-07-06

### Fix — WB UI «зависает» при подключении
- «Подключено» сразу после поднятия TUN (~15 с), не после 30-секундного warmup ipify.
- Warmup probe сокращён до 12 с и идёт в фоне; при неудаче — WARN, без повторного перезапуска UI.
- Лимит спама ошибок `OpenStream` / `remote not ready` в логах (не подвешивает WebView).

## [0.3.179] — 2026-07-06

### Fix — повторный перезапуск после обновления
- Не применять «висящий» `wdtt-new.exe`, если версия уже установлена.
- Очистка `%LOCALAPPDATA%\\WDTT\\update\\` после успешной установки.
- Убран авто-restart через 2 с при старте — только при отключении VPN (если пакет новее текущей версии).

## [0.3.178] — 2026-07-06

### Fix — dual-track + WBT latency display
- **Dual-track**: round-robin по двум VP8-трекам с reorder-буфером (seq) — bandwidth ×2 без ломания KCP.
- **WBT RTT**: сглаженный EWMA в UI (без скачков 3000 ms при speedtest), cap tuning 250 ms.
- Убрана ошибочная подсказка «держите dual-track выключенным»; дефолт снова **on**.

## [0.3.177] — 2026-07-06

### Fix — обновление при активном VPN
- Скачивание работает **с включённым VPN** (GitHub bypass /32 как раньше).
- Если VPN активен — пакет сохраняется, UI: «Ждёт отключения».
- **Автоустановка** при отключении (кнопка питания) или при следующем запуске без VPN.

## [0.3.176] — 2026-07-06

### Perf — WB Stream throughput
- **smux**: окно потока 512 KB → 2 MB, сессии 2 MB → 8 MB — снимает потолок ~20–30 Мбит/с на одно соединение при RTT через WebRTC+KCP.
- **Dual-track**: дефолт выключен; KCP идёт только по track #0, второй трек тратил SFU/CPU без пользы. Подсказка в Settings.

## [0.3.175] — 2026-07-06

### UI — маршрутизация WB и логи
- Убраны пресеты Global / Bypass LAN / RU direct / Custom и подсказка — только таблица правил.
- Скроллбар логов: тонкий, тёмный (WebView2), без белой полосы.

## [0.3.174] — 2026-07-06

### Fix — скачивание обновления через VPN / после disconnect
- GitHub (`api.github.com`, `release-assets…`) — **bypass /32** мимо TUN, напрямую через Wi‑Fi.
- TLS handshake timeout 90 s, 3 повтора; bind на LAN IP.
- Работает даже если split-маршруты TUN ещё не сняты после отключения.

## [0.3.173] — 2026-07-06

### Perf — WB Stream / xray протокол (Windows)
- **VP8 carrier**: дефолт fps 60 / batch 128, coalesce 1400 B — выше потолок пропускной (~70 Mbps теор.).
- **Windows timer**: `TimeBeginPeriod(1)` — pacing не сбивается в пачки по 15 ms (jitter → потери KCP).
- **xray split**: `IPIfNonMatch` вместо `IPOnDemand` — без лишнего DNS; sniff только http/tls.
- **xray**: loglevel error, access log не гоняется через relay/CPU.
- **Probe**: выключен в split-режимах (custom/ru_direct/bypass_lan).

## [0.3.172] — 2026-07-06

### Fix — обновление UI
- Прогресс обновления в global store; нельзя запустить второе скачивание.
- Disconnect → Install без ожидания 25 с teardown.

## [0.3.171] — 2026-07-06

### Fix — WB direct egress on Cyrillic Wi‑Fi (Windows)
- xray: `autoOutboundsInterface` больше не `"auto"` — bind по **ifIndex** физического адаптера (кириллические имена Wi‑Fi ломали direct).
- Direct outbound: `sockopt.interface` = ifIndex когда alias non-ASCII; убирает `Failed to find matching adapter name`.
- Warmup: ipify помечен как proxy egress (не показатель direct-маршрута).

## [0.3.170] — 2026-07-06

### Fix — WB xray routing: direct (geosite/geoip) actually bypasses tunnel
- **Правила UI**: domain + IP в одной строке → два xray-правила (ИЛИ), не AND — иначе `geosite:yandex` + `geoip:ru` не матчились без SNI.
- **xray**: `domainMatcher: hybrid`, Loyalsoldier `geosite.dat`, direct outbound без bind на кириллический Wi‑Fi (`sendThrough` only).
- В логе xray: `rules=N` и `[tun-in -> direct]` для yandex.ru после переподключения.

## [0.3.169] — 2026-07-06

### Feat — WB маршрутизация через xray TUN (Windows)
- Вернул xray-core embed + geoip/geosite, UI «Маршрутизация WB» (пресеты global / bypass_lan / ru_direct / custom).
- WB на Windows: xray TUN → SOCKS → joiner (вместо gVisor netstack); правила v2rayN-style.
- `ConnectWB` снова принимает routing payload; переподключение сохраняет выбранный режим.

## [0.3.168] — 2026-07-06

### Fix — логи: буфер 500 строк
- UI-буфер снова 500 строк (было 10000); полный лог по-прежнему в файле `~/.config/pwdtt/logs/`.

## [0.3.167] — 2026-07-05

### Fix — логи: как раньше
- Убраны батчи и кнопка ↓; строки снова появляются по одной, автопрокрутка как до 0.3.165.

## [0.3.166] — 2026-07-05

### Fix — логи открываются снизу
- При входе на вкладку «Логи» сразу показываются последние строки (сломалось в 0.3.165 из‑за проверки dist&lt;48 при scrollTop=0).

## [0.3.165] — 2026-07-05

### Fix — логи desktop: скролл не дёргается
- Автопрокрутка вниз только если уже внизу; при перетаскивании скроллбара — пауза.
- Тёмный скроллбар (WebView2); кнопка ↓ если проскроллили вверх.

## [0.3.164] — 2026-07-05

### UI — логи
- Буфер логов в UI увеличен до 10000 строк (было 500); полный лог по-прежнему пишется в файл `~/.config/pwdtt/logs/`.

## [0.3.163] — 2026-07-05

### UI — настройки
- Убраны подсказки VP8 pacing и VK cookies (анонимный вход / remixsid) — остаётся только предупреждение об устаревших cookies.

## [0.3.162] — 2026-07-05

### Fix — страницы не грузятся во время download
- **KCP ordering**: dual-track `SendRaw` больше не round-robin — один VP8-трек для KCP (reorder между треками ломал smux).
- **Congestion flush**: мелкие пакеты и глубокая очередь → flush 1 ms вместо 3 ms batch.
- **smux**: буфер stream 512 KB (было 2 MB) — bulk download не съедает весь pipe.

## [0.3.161] — 2026-07-05

### Fix — zombie-туннель после idle (стабильность как VK)
- **Zombie-детект по download**: туннель считается «зависшим» только когда **RX не растёт 35+ с** и probe падает — upload-trickle (keepalive) больше не маскирует мёртвый download.
- **Post-recovery verify**: если WebRTC rebind не восстановил TCP за 25 с → **полное переподключение** (не бесконечный rebound с 0 B/s).
- **Сервер**: creator rejoin после tunnel lost — 1 с вместо 3 с (быстрее resync KCP после client recovery).

## [0.3.160] — 2026-07-05

### Fix — таймер сессии + iOS ошибки
- **PC**: uptime не сбрасывается при переходе Connect ↔ Logs (`connectingSince` в store, `connectedAtMs` не затирается нулём).
- **iOS 0.4.8**: красный текст ошибки на экране подключения; таймер сессии; VK — явные сообщения при неполном профиле / нет hash.

## [0.3.159] — 2026-07-05

### WB Stream — Dual VP8 track + игры (UDP)
- **Dual-track end-to-end**: настройка «Dual VP8 track» из UI → `tracks=2` в логах, KCP `SendRaw` round-robin по cam + screenshare.
- **Сервер (wbcreator)**: creator публикует 2 VP8-трека для download.
- **UDP для игр (CS2/Dota)**: туннель переведён с request/response на **двусторонний streaming** — Steam SDR / Dota ping получают unsolicited datagrams от сервера (раньше все UDP ответы терялись → «Latency: ERROR» / «Failed to reach official servers»).

## [0.3.158] — 2026-07-05

### WB Stream — плавнее speedtest (меньше рывков скорости)
- **KCP RTT EWMA**: тюнинг по сглаженному RTT — одиночный всплеск 800+ ms не переключает interval 20→50.
- **VP8 keyframe**: не вставляется при активной очереди данных (не крадёт полосу mid-download).

## [0.3.157] — 2026-07-05

### WB Stream — стабильный канал на мобильном + throughput
- **KCP→VP8 batching**: несколько KCP-сегментов в одном VP8 sample (меньше WriteSample/s, выше Mbps).
- **RTT-adaptive KCP**: interval/window подстраиваются под RTT (40–50 ms interval при RTT>250 ms — меньше ложных retransmit).
- **smux**: stream buffer 2 MB, keepalive 15s/60s (быстрее детект NAT-dead на мобильном).
- **Proactive recovery**: авто KCP+smux restart при RTT>700 ms >10s; nudge recovery при RTT=0.
- **Probe**: интервал health-check 14–22s при высоком RTT — меньше ложных переподключений.

## [0.3.156] — 2026-07-05

### WB Stream — скорость и VP8 без искусственных лимитов
- **FPS/Batch**: сняты потолки 30/48 в клиенте и wbjrunner — настройки из UI передаются как на iOS (до 120 fps / 256 batch).
- **KCP**: окна snd=2048 / rcv=4096 для высокого RTT (мобильный интернет ~300 ms).
- **VP8 coalesce**: max plaintext 1200 байт на sample (было 960).
- **Дефолты**: fps=30, batch=64 (как iOS).
- **Сервер (wbcreator)**: creator pacing 60/128 — быстрее download через туннель после обновления wdtt на VPS.

## [0.3.155] — 2026-07-05

### Fix — CI/build после pre-xray restore
- wbstream: восстановлены `routes_windows.defaultIPv4Egress` и helper-функции в `prepare_windows` (нужны для сборки wbxray/routeshell, хотя runtime — netstack-only).
- `PrepareBeforeStart` — лёгкий pre-xray вариант (без xray pool cleanup).

## [0.3.154] — 2026-07-05

### Rollback — полный откат WB-кода до pre-xray (v0.3.125)
- **Проблема**: v0.3.152–153 отключали xray в runtime, но wbstream-wbt оставался с xray-эра изменениями (random adapter `WDTT-WB-*`, RouteShell, тяжёлый prepare). Скорость ~1.5 MB/s вместо прежних значений.
- **Fix**: восстановлены **исходники до xray** из git:
  - `wbjrunner/runner.go`, `bypass.go`, `warmup.go` — netstack-only, фиксированный адаптер `WDTT-WB`
  - `desktoptun/prepare_windows.go`, `routes_windows.go`, `emergency_windows.go` — pre-xray
  - `backend/wb.go` — pre-xray (25s shutdown wait, без UseXray/routing)
- **Disconnect**: фоновый `awaitShutdown` — снимает зависший WDTT-WB из трея Windows после отключения.
- xray embed/UI по-прежнему удалены (v0.3.153).

## [0.3.153] — 2026-07-05

### Rollback — полное удаление xray из клиента
- **Аудит v0.3.152**: в логах уже был `mode=wbt (netstack)` и `direct netstack up` — xray **не запускался**, но оставался в бинарнике (~52 MB embed: xray.exe, geoip.dat, geosite.dat).
- **Удалено**: embed xray, `InitXray`, `prepareWBXray`, CI `fetch-xray-assets.sh`, UI/типы маршрутизации (`WBRouting`, `wbRouting.ts`).
- **wbstream-wbt**: адаптер снова фиксированный `WDTT-WB` (не `WDTT-WB-<random>` — это было для xray pool collision).
- WB = только gVisor netstack + tun2socks + WebRTC/KCP, как до v0.3.126.

## [0.3.152] — 2026-07-05

### Rollback — xray TUN → gVisor netstack, убрана маршрутизация WB
- **Симптом**: скорость упала с ~30 МБ/с до 5–7 МБ/с после перехода на xray TUN + routing/sniffing.
- **Откат**: Windows WB снова использует **gVisor netstack + tun2socks** (`UseXray=false`), как до v0.3.x xray-ветки.
- **UI**: убрана кнопка «Маршрутизация WB» и редактор правил — весь трафик идёт через туннель (global).
- xray-бинарник больше не стартует при WB-подключении на Windows.

## [0.3.151] — 2026-07-05

### Fix — WB не отключался при переключении на VK (и наоборот)
- **Симптом**: пользователь переключился с WB на VK, но WB-туннель оставался живым в той же комнате. Результат: два publisher в одном room → шторм peer-restart → просадка скорости. Также при обратном сценарии (переключение с VK на WB) оба туннеля сосуществовали и делили маршруты.
- **Корень**: `App.Connect` и `App.ConnectWB` — два независимых менеджера (`Orchestrator` + `WBManager`). Ни один не останавливал другого. Фронтенд выбирал, какой из двух дисконнект вызвать, на основе `tunnelProtocol` из стора; при смене протокола в настройках во время активной сессии выбор был неправильным.
- **Fix** (`backend/app.go`):
  - `Connect` (VK): если WB запущен — вызывает `wb.Disconnect()` перед стартом VK.
  - `ConnectWB` (WB): если VK-orch запущен — вызывает `orch.Stop()` перед стартом WB.
  - Гарантируется взаимная исключительность на уровне Go-бэкенда, независимо от действий UI.

## [0.3.150] — 2026-07-05

### Fix — шторм peer-restart (`KCP+smux restarted`) + просадка скорости
- **Симптом** (лог с iOS): бесконечное чередование двух epoch-ов (`0x1e493b4d` ↔ `0x1b5a86ad`) с `peer restart detected` → `KCP link ready` → `KCP+smux restarted` по несколько раз в секунду.
- **Корень**: два *живых* publisher-а в одной комнате (напр. Windows-клиент и iOS зашли в **одну и ту же room** — нарушение схемы 1:1 creator↔joiner). Обфускатор на приёме дёргался между их epoch-ами, и каждый флип ронял KCP+smux. Постоянный ресет KCP = убитые TCP-стримы и обнулённое congestion-окно → **это и есть причина падения скорости 30→5-7 МБ/с**.
- **Fix** (`tunnel/obfuscator.go`, общий для server/Windows/iOS): **sticky-peer**. Обфускатор фиксируется на текущем peer-е и принимает другой epoch только после реальной тишины (`peerHandoverSilence=2s`, настоящий reconnect). Кадры конкурирующего publisher-а игнорируются без ресета KCP. Легитимный reconnect (старый peer замолчал) по-прежнему делает ровно один restart.
- Тесты: `TestObfuscatorStickyPeerNoStormWhileActive` (два publisher-а 10s → 0 restart), обновлён `TestObfuscatorIgnoresStaleEpochAfterAdvance` (handover после тишины).

> ⚠️ Операционно: не подключай две устройства к **одной** WB-комнате одновременно — каждому клиенту нужна своя room. Sticky-peer лишь предотвращает разрушительный шторм, но трафик всё равно уйдёт только через того peer-а, на кого клиент «залочился».

## [0.3.149] — 2026-07-05

### Fix — RU-трафик шёл через VPS вместо провайдера
- **Корень**: `routing.domainStrategy=IPIfNonMatch` + catch-all правило `{network:tcp,udp → proxy}`. Sniffing подменяет цель на домен, `IPIfNonMatch` откладывает проверку `ip`/`geoip:ru` на второй проход, который запускается только если ничего не совпало на первом. Но catch-all по `network` совпадает **на первом проходе** → всё уходит в `proxy` (VPS) ещё до того, как `geoip:ru` вообще проверится.
- **Fix**: `domainStrategy=IPOnDemand` — `geoip:ru` резолвится/матчится **inline** на правиле RU, до catch-all. RU-адреса теперь идут через `direct` (провайдер), остальное — через туннель.
- Правила `Direct`/`Block` пользователя (в т.ч. `geoip:ru`, `geosite:category-gov-ru`) теперь реально применяются.

## [0.3.148] — 2026-07-05

### Fix — уникальное имя wintun-адаптера + ifIndex-настройка
- **Корень 0x800700B7**: фиксированное имя `WDTT-WB` коллизилось с зависшим wintun-pool от прошлой сессии → xray падал, а мы «находили» мёртвый leftover-адаптер. Теперь имя **уникальное на запуск** (`WDTT-WB-<rand>`) — CreateAdapter всегда чистый. Детект/настройка по ifIndex+описанию, поэтому имя не важно.
- **`Enable-NetAdapter -InterfaceIndex`** — у cmdlet нет такого параметра; теперь через `Get-NetAdapter -InterfaceIndex | Enable-NetAdapter`.
- **`WaitAdapterUp` по имени убран** из setup (падал на локализованном alias) — проверка готовности по ifIndex внутри FinishTunSetup.
- **Детект ifIndex** — предпочитает live `Status=Up` Xray/Wintun устройство (не stale ghost).

## [0.3.147] — 2026-07-05

### Fix — WB TUN настраивается по ifIndex, не по alias
- v0.3.146 `Rename-NetAdapter` молча падал (alias `WDTT-WB` зарезервирован скрытым ghost-устройством) → TUN так и не находился по имени.
- **Настройка TUN по `InterfaceIndex`**: IP/маршруты/metric/MTU/DNS через `New-NetIPAddress`/`New-NetRoute`/`Set-NetIPInterface` по ifIndex — не зависит от локализованного alias.
- **Детект готовности** — по live Xray/Wintun netdev (описание), а не по строгому имени.
- **Purge ghost** — удаление non-present (`Status ≠ OK`) Xray/Wintun PnP-устройств, освобождает alias; rename в `WDTT-WB` теперь best-effort (для UI), функционал от него не зависит.

## [0.3.146] — 2026-07-05

### Fix — WB TUN: адаптер Up, но с локализованным именем
- **Корень бага 0x800700B7**: xray **успешно** создаёт wintun-адаптер (Up, `InterfaceDescription=Xray Tunnel`), но Windows даёт ему локализованный alias (`Подключение по локальной сети`), а не `WDTT-WB`. Детект по имени не находил → мы сносили рабочий адаптер → пересоздание падало с «file already exists».
- **`EnsureTunAdapterReady`** — находит live Xray/Wintun netdev по `InterfaceDescription` и **переименовывает** в `WDTT-WB` (`Rename-NetAdapter` по ifIndex), не снося адаптер.
- Детект готовности TUN теперь через rename, а не строгое совпадение имени.

## [0.3.145] — 2026-07-05

### Fix — WB connect hang (45s+ before SOCKS/xray)
- **Тройной тяжёлый wintun cleanup** на каждый connect (await 25s + prepareWBTun + EnsureWintunPoolFree с pnputil) — убран.
- **Fast path**: QuickReleaseWintunPool (~300ms) на первой попытке; deep cleanup + pnputil только на retry после exit 23.
- **awaitShutdown** 25s → 8s, emergency cleanup сразу при reconnect.
- **prepareWBTun** — только extract wintun.dll, без PowerShell на connect.

## [0.3.144] — 2026-07-05

### Fix — WB Stream TUN (Windows), ghost Xray Tunnel
- **`0x800700B7` / exit 23** — xray не мог создать `WDTT-WB`: kernel pool занят, NetAdapter — disconnected `Xray Tunnel` с локализованным именем.
- **`EnsureWintunPoolFree`** — OpenAdapter+Close с retry, probe CreateAdapter, `pnputil` для зависших PnP-устройств.
- **Cleanup по InterfaceIndex** — удаление Xray/Wintun netdev без привязки к имени `WDTT-WB`.
- **Быстрый retry** — при раннем exit xray (exit 23) сразу prep+retry, без слепого ожидания 20s.

## [0.3.143] — 2026-07-05

### Fix — WB Stream TUN (Windows)
- **Ghost wintun pool** — перед xray закрываем orphaned kernel-адаптер `WDTT-WB` (`ReleaseWintunPool`), иначе xray открывает «мёртвый» pool без NetAdapter → timeout 20s и APIPA `169.254.x`.
- **Cleanup** — при disconnect/teardown и retry: pool release + удаление Wintun/Xray адаптеров в PowerShell.
- **Диагностика** — при timeout в лог список видимых wintun-адаптеров.
- **xray cwd** — `cmd.Dir` = каталог exe (wintun.dll рядом с xray).

## [0.3.142] — 2026-07-05

### Fix
- **parse egress route** — PowerShell иногда отдаёт `LocalIP` как object; гибкий парсер + тесты.
- **SOCKS accept loop** — выход при закрытом listener (без спама ×165k).

## [0.3.141] — 2026-07-05

### Fix
- CI Windows: `defaultIPv4Gateway` compile error (undefined `gw`).

## [0.3.140] — 2026-07-05

### WB Stream — VP8 настройки из UI
- **FPS/Batch** передаются в `ConnectWB` (раньше игнорировались).
- UI: FPS max 30, Batch max 48 — совпадает с лимитом TUN MTU.
- Лог: `[wbt] vp8 batch capped N→48 (tun mtu)` при обрезке.

## [0.3.139] — 2026-07-05

### WB Stream xray — fix TUN hang / adapter timeout
- **Egress IP** через InterfaceIndex + fallback (192.168.x) — больше не `ip=` пустой.
- **autoOutboundsInterface** всегда `auto` на TUN; explicit bind только при наличии local IP.
- **prepXray** + `EnsureAdapterAbsent` перед стартом; stdout xray в лог.

## [0.3.138] — 2026-07-05

### WB Stream — меньше лишних действий при connect
- Bypass-хосты резолвятся **один раз** после auth (без 3–4 повторов в логе).
- **PrepareBeforeStart** — один раз перед xray, повтор только при retry.
- `resolveHostForce` молчит, если IP не изменились.

## [0.3.137] — 2026-07-05

### WB Stream — Direct (RU) через провайдера
- **freedom outbound** привязан к физическому NIC (`sockopt.interface` + `sendThrough`) — RU/direct больше не уходит обратно в TUN и на VPS.
- В логах: `[xray] direct egress via "Wi-Fi" ip=192.168.x.x`.

## [0.3.136] — 2026-07-05

### WB Stream — фиксированный адаптер WDTT-WB
- Всегда одно имя **WDTT-WB** (без `-PID`).
- **TeardownTunAdapter** при disconnect и закрытии приложения — Remove-NetAdapter, не только disable.
- Legacy `WDTT-WB-*` удаляются при cleanup.

## [0.3.135] — 2026-07-05

### WB Stream xray — adapter Disabled fix
- xray создаёт WDTT-WB-* в состоянии **Disconnected** — `FinishTunSetup` теперь **Enable-NetAdapter** + **setAdapterIP** (как legacy tun2socks), затем split routes.
- Ждём **present** (адаптер есть), не Up+IP — настройка через RouteShell.
- Cleanup: удаляет offline Wintun/Xray Tunnel orphans; ctx-aware retry; текст ошибки WDTT-WB*.

## [0.3.134] — 2026-07-05

### WB Stream xray — wintun pool collision
- **Адаптер `WDTT-WB-<pid>`** — свежий wintun pool каждый запуск, без «file already exists» от прошлой сессии.
- **CleanupAllWDTTAdapters** — удаляет все `WDTT-WB*` перед стартом и при shutdown.
- **ICE filter** — не bind на 169.254.x (ghost APIPA).
- Retry: 2.5 с пауза после kill xray + повторная очистка.

## [0.3.133] — 2026-07-05

### WB Stream xray — ghost adapter fix
- **Remove-NetAdapter** вместо только Disable: зависший WDTT-WB (169.254.x) больше не блокирует xray «file already exists».
- **Retry**: при timeout — cleanup + повторный старт xray.
- **Shutdown**: полное удаление адаптера после отключения.

## [0.3.132] — 2026-07-05

### WB Stream xray — быстрый подъём TUN
- **Без 25 с ожидания**: split-маршруты ставятся сразу после `Creating adapter` (~1–2 с), как в legacy tun2socks.
- **RouteShell.FinishTunSetup**: metric, MTU, IPv6 off, DNS off на адаптере.
- xray только поднимает wintun + gateway; маршруты — наш RouteShell (autoSystemRoutingTable убран).

## [0.3.131] — 2026-07-05

### WB Stream xray — Windows маршруты
- **xray 26 TUN config**: `gateway` + `autoSystemRoutingTable` + `autoOutboundsInterface` вместо устаревших `autoRoute`/`inet4_address` (на Windows они игнорировались — split routes не ставились).
- **Fallback**: если xray не успел — `RouteShell` ставит `0.0.0.0/1` + `128.0.0.0/1` вручную.

## [0.3.130] — 2026-07-05

### WB Stream xray — egress IP fix
- **Warmup ждёт xray TUN**: перед пробой IP — `WaitTunRoutingReady` (адаптер Up + split-default routes), не фиксированные 600 ms.
- **Не «готов» на домашнем IP**: если пробный запрос возвращает pre-tun egress (как ICE srflx), повтор до смены IP или timeout.

## [0.3.129] — 2026-07-05

### WB Stream — UI и логи
- **Маршрутизация**: без пресетов и подсказок; пустая таблица по умолчанию; тёмные скроллбары.
- **Sidebar**: ID устройства и версия снизу (убрано из Настроек).
- **Логи**: полная диагностика (KCP, xray, ICE) — без per-frame VP8-спама.

## [0.3.128] — 2026-07-05

### WB Stream — редактор маршрутизации
- **Отдельная настройка** (иконка ↗ в sidebar при протоколе WB): таблица правил как v2rayN/панель.
- **Пресеты** Global / Bypass LAN / RU direct загружают шаблон в таблицу; правила редактируются.
- **Поля правила**: outbound (Proxy/Direct/Block), domain, IP, port, network; порядок ↑↓.
- **geosite.dat** в bundle для `geosite:ru`, `geosite:private`.

## [0.3.127] — 2026-07-05

### WB Stream xray — hotfix Windows
- **xray exit 23**: `wintun.dll` рядом с `xray.exe` (из zip Xray + embed) — без него TUN падал через ~2 с.
- **PowerShell**: `PrepareBeforeStart` / disable adapter — скрытое окно (`CREATE_NO_WINDOW`).
- **UI**: игнор VK `disconnected` при активном протоколе WB (ложное «— Отключено»).
- **Логи xray**: stderr subprocess в UI.

## [0.3.126] — 2026-07-05

### WB Stream — xray TUN внутри приложения
- **Архитектура как на телефоне + v2rayN**: `wintun → xray-core → SOCKS joiner → WebRTC/KCP` вместо gVisor netstack.
- **Presets маршрутизации** (Настройки → WB): Global, Bypass LAN, RU direct (`geoip:ru` + `geoip.dat`).
- **Встроены** `xray.exe` + `geoip.dat` (~52 MB); bypass signaling/ICE по-прежнему через OS `/32` маршруты.
- **Recovery** (SwapTunnel, bypass auth) без изменений.

## [0.3.125] — 2026-07-04

### WB Stream — zombie recovery (iOS-style)
- **Auth/signaling вне netstack**: `guest-register` и LiveKit dial идут по IP bypass-маршрута (как iOS `ResolveHost`), не через мёртвый gVisor при живом WDTT-WB.
- **Joiner не убивается** при soft recovery — `SwapTunnel` на новом WebRTC carrier (как iOS «keeping SOCKS sessions»).
- **Zombie → сразу session rebind**, без бесполезного KCP-only.

## [0.3.124] — 2026-07-04

### WB Stream — Windows TUN
- **wintun.dll**: перезаписывается, если размер не совпадает с встроенным (после автообновления мог остаться старый/битый dll).
- **Перед VPN**: очистка маршрутов WDTT-WB + отключение зависшего адаптера; повторная попытка `Start()` при ошибке `create tun`.
- **UI**: при провале TUN — явная ошибка, не «вечное подключение».

## [0.3.123] — 2026-07-04

### WB Stream — реконнект при мёртвом VPN
- **Direct DNS для bypass**: при живом, но мёртвом туннеле `stream.wb.ru` резолвится через 1.1.1.1/8.8.8.8 напрямую — больше нет 10+ минут `no such host` и бессмысленных retry.
- **Stale cache**: если DNS временно недоступен, старые IP bypass-маршрутов сохраняются (не удаляются перед lookup).
- **Эскалация soft recover**: zombie / RTT=0 → сразу WebRTC-сессия; иначе 1-я попытка KCP+smux, 2-я — session; cooldown 15 с когда RTT мёртв.
- **Relay**: `RecoverRequest.ForceSession`, `RestartLink(force)` обходит debounce при watchdog.

### VK login (Windows)
- **Окно не закрывается при remount**: native WebView2 больше не убивается при размонтировании модалки (React Strict Mode).
- Cookies сохраняются сразу в helper при `status=done`, не только при poll UI.

## [0.3.122] — 2026-07-04

### WB Stream — zombie tunnel + rebind fix
- **Zombie-детект**: probe fail при trickle (1–16 KB/s) → soft recover через 2 попытки, не висим с «зелёным» мёртвым туннелем.
- **Meaningful traffic**: probe игнорируется только при реальной нагрузке ≥32 KiB/s (не keepalive/trickle).
- **Soft recover**: сначала KCP+smux restart без снятия VPN, потом WebRTC session.
- **Relay**: после `carrier rebind` всегда перезапуск KCP+smux (старые smux-потоки ломали новые TCP → `ERR_CONNECTION_CLOSED`).

## [0.3.121] — 2026-07-04

### WB Stream — бесшовное восстановление
- **Probe не рвёт активную загрузку**: если байты через туннель ещё идут, неудачный HTTP-probe больше не считается «мёртвым туннелем» (исправлен ложный реконнект при скачивании 200+ MB).
- **Grace 90 с после carrier rebind**: после `sub offer`/перепривязки KCP probe временно отключён — путь успевает восстановиться.
- **Мягкое восстановление**: до 2 раз переподключается только WebRTC/KCP **без снятия VPN-адаптера** (маршруты остаются, интернет не «вылетает»). Полный реконнект — только если мягкое не помогло.
- UI при внутреннем rebind больше не переключается в «Подключение…».

## [0.3.120] — 2026-07-04

### Главный экран
- **Время подключения**: «Подключение: M:SS» во время dial, «Подключено: M:SS» после поднятия туннеля (VK и WB).
- **VK воркеры**: «Воркеры: активные/назначенные» (например `7/36`) — только для протокола VK.

## [0.3.119] — 2026-07-04

### VK login (Windows)
- **Белый экран**: WebView2 после Embed не был видим — добавлены `Hide()`/`Show()` (как в Wails), теперь vk.com реально отображается.
- **Ложное закрытие ~8 с**: гостевой `remixsid` при редиректах больше не считается входом — нужны **remixsid + p**, проверка `web_token`, два подтверждения подряд.
- Лог: `%APPDATA%/pwdtt/webview-vk/vk-login.log`.

## [0.3.118] — 2026-07-04

### VK login (Windows)
- Исправлена сборка v0.3.117 (helper `vkRemixsidIsNew` в общем пакете).

## [0.3.117] — 2026-07-04

### VK login (Windows)
- **Окно не закрывается через 2 секунды**: перед входом очищается профиль WebView2; cookies принимаются только после загрузки vk.com и только если `remixsid` **новый** (не из прошлой сессии). Раньше stale `remixsid` из `%APPDATA%/pwdtt/webview-vk/profile` сразу закрывал окно с белым экраном.

## [0.3.116] — 2026-07-04

### WB Stream — детект «мёртвого» data path
- **HTTP-probe через туннель**: KCP RTT может оставаться ~90 ms, пока новые TCP-соединения уже не проходят (сайты `ERR_CONNECTION_CLOSED`, старые загрузки ещё идут). Клиент каждые 10 с проверяет реальный HTTP через VPN (IPv4-only, как warmup); 3 подряд неудачи → полный реконнект.
- Unit-тесты `wbTunnelDead` / probe limit.

### VK login + обновления (Windows)
- **`execDetachedUI`**: VK worker и relaunch после updater запускаются без `HideWindow` — первый `ShowWindow` больше не прячет окно.
- **WebView2**: двойной `ShowWindow(SW_SHOWNORMAL)` + `SWP_SHOWWINDOW`; подсказка в модалке про окно «WDTT — вход VK» на экране / в панели задач.

## [0.3.115] — 2026-07-04

### VK login (Windows)
- **Откат iframe**: `id.vk.com` запрещает показ во фрейме (X-Frame-Options → «отказано в подключении»). Вход снова в отдельном окне WebView2.
- **Окно всегда видно**: открывается по центру экрана и поверх всех окон (TOPMOST) — раньше пряталось за главным окном.

## [0.3.114] — 2026-07-04

### Обновления (Windows)
- **Окно после обновления**: перезапуск с `--show-window` — приложение открывается на экране, а не только в трее.

## [0.3.113] — 2026-07-04

### VK login (Windows)
- **Вход прямо в окне приложения**: vk.com открывается в iframe внутри модалки «Вход в VK» (как на Linux), а не в отдельном невидимом окне WebView2.

## [0.3.112] — 2026-07-04

### WB Stream — авто-восстановление туннеля
- **Watchdog живости**: если 30 секунд нет живого RTT (туннель умер, внутренний реконнект долбится в мёртвый netstack — `guest-register EOF`), клиент сам полностью сносит туннель и подключается заново к новой сессии.
- **Авто-реконнект при падении**: если туннель завершился сам (не кнопкой) — автоматическое переподключение вместо «Отключено».
- Кнопка отключения всегда побеждает авто-реконнект (нет гонок).

### Обновления (Windows) — переписаны с нуля
- **Self-apply**: скачанный exe сам ждёт выхода старого, копирует себя на место и перезапускает приложение. Без cmd/vbs/powershell — нет консоли, нет второго UAC, нет двойного запуска.
- Лог: `%LOCALAPPDATA%\WDTT\update\apply.log`; при сбое копирования старый exe восстанавливается.

### VK login (Windows)
- **«COM init: Incorrect function»**: убран повторный `CoInitializeEx` (COM уже инициализирован WebView2-пакетом; повторный вызов возвращает S_FALSE, который выглядел как ошибка).

## [0.3.111] — 2026-07-04

### VK login (Windows) — как на iOS
- **Нативное окно WebView2** вместо Edge/chromedp: тот же exe в режиме `--vk-login-worker` открывает окно с `https://vk.com/` (как WKWebView на iOS в vk-turn-proxy-ios).
- Cookies (`remixsid`, `p`, включая HttpOnly) читаются через `ICoreWebView2CookieManager` — **не нужен DevTools-attach**, работает и под администратором.
- Убраны chromedp / `--no-sandbox` / schtasks-деэлевация — больше нет `about:blank`, «chrome failed to start» и мигающей консоли.
- Требуется Microsoft Edge WebView2 Runtime (в Windows 10/11 предустановлен).

## [0.3.110] — 2026-07-04

### VK login (Windows)
- **Edge chromedp**: убран `--no-sandbox` — окно больше не открывается на `about:blank` с предупреждением и ошибкой «chrome failed to start».

### Обновления (Windows)
- **Updater**: полностью скрытый запуск (wscript, без echo/pause); один перезапуск через PowerShell Hidden + UAC (без explorer/RunOnce — не два окна wdtt).

## [0.3.109] — 2026-07-04

### WB Stream
- **KCP restart storm**: debounce peer-epoch restart (3s) на joiner/creator; debounce sub-offer rebind (5s).

### Обновления (Windows)
- **Updater**: `cmd start /MIN` detached (без schtasks) + RunOnce fallback + explorer restart + UAC.

### VK login (Windows)
- **schtasks /RU %USERNAME% /RL LIMITED**; worker не блокируется если elevated — пробует Edge.

## [0.3.108] — 2026-07-04

### Обновления + VK login (Windows)
- **Updater**: `schtasks` one-shot task (не дочерний процесс) — переживает выход wdtt; видимое окно «WDTT Update»; проверка размера файла; перезапуск PowerShell RunAs + explorer; лог `%LOCALAPPDATA%\\WDTT\\update\\apply.log`.
- **VK de-elevation**: worker через `schtasks /RL LIMITED` — реальный medium integrity, Edge стартует.

## [0.3.107] — 2026-07-04

### VK login (Windows)
- **Edge chromedp**: явные флаги без `DefaultExecAllocatorOptions` (убран скрытый headless/disable-gpu).
- **Профиль**: отдельная `session-*` папка на каждый вход — нет блокировок от admin-сессии.
- **Worker**: проверка de-elevation; лог `%APPDATA%\\pwdtt\\webview-vk\\edge.log`.
- **De-elevate**: VBS запускает worker с видимым окном (style 1).

## [0.3.106] — 2026-07-04

### Обновления (Windows)
- **Updater живёт после выхода**: bat запускается через detached wscript — не убивается вместе с wdtt.
- **Copy move/rename**: старый exe → `.old`, до 90 попыток; откат при ошибке.
- **Перезапуск**: `Start-Process -Verb RunAs` — явный UAC-диалог; `os.Exit(0)` вместо `runtime.Quit`.
- **Ошибка**: MessageBox с путём к `%TEMP%\\wdtt-update\\apply.log`.

## [0.3.105] — 2026-07-04

### VK login (Windows, admin)
- **De-elevation через explorer.exe**: VBS запускается через shell explorer (medium integrity) — без `CreateProcessAsUser` / `WTSQueryUserToken`, которые требуют привилегий.
- **Stop VK**: worker PID в status.json, остановка через `taskkill /T`.

## [0.3.104] — 2026-07-04

### Обновления (Windows)
- **In-app update**: bat-скрипт ждёт завершения процесса (PID), до 60 попыток copy с паузой — exe больше не блокируется.
- **Перезапуск**: `start "" exe` вместо VBS `runas` — приложение открывается после установки.
- **Проверка**: скачанный файл проверяется (PE + размер ≥ 8 MB); лог `%TEMP%\\wdtt-update\\apply.log`.

## [0.3.103] — 2026-07-04

### VK login (Windows, один exe)
- **Без `wdtt-vk-login.exe`**: при запуске от администратора Edge открывается через скрытый режим `--vk-login-worker` того же exe.
- **De-elevation**: `WTSQueryUserToken` + fallback explorer token; включение `SeIncreaseQuotaPrivilege` перед `CreateProcessAsUser` — исправляет «A required privilege is not held by the client».
- **Обновления**: скачивается только `wdtt-windows-amd64.exe`.

## [0.3.102] — 2026-07-04

### WB Stream (стабильность после burst)
- **KCP MTU 1200** (было 1400) — меньше фрагментации VP8/RTP после больших загрузок.
- **VP8 batch 48** на creator (как у joiner UseTUN) — выравнивание размера кадров.
- **Activity по carrier**: joiner считается online по VP8 keepalive, не только smux-трафику.
- **KCP restart** только после реального tunnel lost, не при idle timeout (10 мин).

## [0.3.101] — 2026-07-04

### VK login (Windows, admin)
- **De-elevation helper**: при запуске от администратора Edge/chromedp стартует через `wdtt-vk-login.exe` (medium-integrity token explorer.exe); статус в `%APPDATA%/pwdtt/webview-vk/status.json`.
- **`wdtt-vk-login.exe`**: снова в релизе, manifest `asInvoker`, те же chromedp-опции что и in-process.

### Обновления
- **Restart runas**: после скрытого copy VBS перезапуск через `Shell.Application.ShellExecute` с verb `runas` — приложение остаётся с правами администратора.
- **Helper в апдейте**: `wdtt-vk-login.exe` скачивается и копируется рядом с основным exe.

## [0.3.100] — 2026-07-04

### VK login (Windows)
- **Edge «chrome failed to start»**: chromedp на базе DefaultExecAllocatorOptions с явным headless=false, флаг `edge-skip-compat-layer-relaunch`, снятие DevToolsActivePort из profile lock; понятная ошибка с советом не запускать от администратора и путь к edge.log.

## [0.3.99] — 2026-07-04

### VK login (Windows)
- **Edge видимый запуск**: chromedp без DefaultExecAllocatorOptions/headless — явные флаги visible-браузера, снятие profile lock, WSURLReadTimeout 30s, navigate timeout 45s.
- **Диагностика**: лог Edge в `%APPDATA%/pwdtt/webview-vk/edge.log`.
- **UI**: статус из poll (800ms), ошибки красным.

## [0.3.96] — 2026-07-04

### VK login (Windows)
- **Видимое окно Edge**: chromedp больше не запускается в headless — окно VK реально появляется.

## [0.3.95] — 2026-07-04

### Обновления
- **Сообщение при подключении**: кнопка «Установить» показывает «Сначала отключитесь…», а не просто блокируется.
- **Прогресс скачивания**: полоска и процент в настройках и баннере обновления.
- **Без консоли**: установка через скрытый VBS-скрипт — окно cmd больше не мигает.

### VK login (Windows)
- **Один exe**: вход VK через Edge/chromedp внутри приложения — `wdtt-vk-login.exe` больше не нужен.

## [0.3.94] — 2026-07-04

### VK login (Windows)
- **Как на iOS**: отдельное окно WebView2 с прямым `https://vk.com/` (не iframe+прокси).
- **`wdtt-vk-login.exe`**: helper рядом с основным exe — читает HttpOnly cookies (`remixsid`, `p`) из WebView2.
- Исправлено «id.vk.com отказано в подключении» при QR-входе.

## [0.3.93] — 2026-07-04

### VK login (Windows)
- **WebSocket proxy**: QR-вход VK через id.vk.com теперь проксируется (раньше WS шёл мимо прокси и cookies не собирались).

### UI
- **Настройки**: скроллбар уже (6px), больше отступ справа — не перекрывает тумблеры и кнопки.

## [0.3.92] — 2026-07-04

### VK login (Windows)
- **Прокси VK**: перехват `location.assign/replace/href`, `window.open`, WebSocket — QR-вход больше не обходит прокси.
- **Cookies**: достаточно `remixsid` (как в creds); после входа тумблер VK cookies включается автоматически.
- **iframe**: убран sandbox, блокировавший редиректы VK.

### Обновления
- **In-app update**: кнопка «Установить» — скачивает exe с GitHub и перезапускает (только когда VPN отключён).

### UI
- **Настройки**: скролл внутри модалки, не перекрывает тумблеры; полоса прокрутки с отступом.

## [0.3.91] — 2026-07-04

### CI
- **GitHub Actions**: Linux + Windows собираются на CI (`build-desktop.yml`), релизные файлы `wdtt-linux-amd64` и `wdtt-windows-amd64.exe`.

## [0.3.90] — 2026-07-03

### Обновления
- **Проверка версии**: при запуске баннер, если на GitHub есть новее релиз (`ildarmaga/pwdtt-client`).
- **Настройки → Версия**: текущая версия + кнопка «Проверить обновления» (открывает ссылку на exe).

### VK — вход как на iPhone
- **«Войти через VK»** в настройках (VK протокол): встроенное окно vk.com через локальный прокси,
  автосбор `remixsid` + `p` и сохранение cookies (как iOS `VKAuthWebView`).

### UI
- **Настройки**: убран горизонтальный скролл — только прокрутка вверх/вниз, длинные пути и тексты переносятся.

## [0.3.89] — 2026-07-03

### WB Stream — реконнект на ПК не поднимался (guest-register TLS timeout)
- **awaitShutdown**: 5 s → 25 s. Предыдущий туннель (gVisor drain + снятие split-default
  маршрутов WDTT-WB) при зависшем линке рвётся дольше 5 s. Раньше новый Connect
  стартовал до снятия маршрутов мёртвого адаптера → `guest-register` к `stream.wb.ru`
  уходил в дохлый туннель и падал по TLS handshake timeout в бесконечном цикле.
  Теперь ждём полного последовательного teardown (как iOS) перед новым подключением.
- **Сервер (panel.db)**: сняты лимиты трафика (`total_bytes=0`) — WB работает без квоты.

## [0.3.88] — 2026-06-30

### WB Stream — трафик в панели + egress как VK
- **Сервер**: убран force-NL для WB SOCKS — xray маршрутизирует как VK (RU direct / NL по правилам).
- **Сервер**: трафик WB flush каждые 3 с (не только при закрытии TCP).
- **Клиент**: TCP dial timeout 20 s.

## [0.3.87] — 2026-06-30

### WB Stream — lordfilms / ISP DNS + browser параллель
- **DNS**: снова через роутер (192.168.x.1), как VK — зеркала `*.lordfilms.ru` резолвятся.
- **IPv6**: по-прежнему off на физ. адаптерах (v0.3.86).
- **TCP**: до 1024 параллельных соединений, ожидание слота вместо silent drop.

## [0.3.86] — 2026-06-30

### WB Stream — браузер через туннель (IPv6 + DNS)
- **IPv6**: при подключении отключается ms_tcpip6 на физических адаптерах — Chrome больше не обходит VPN по v6.
- **DNS**: 1.1.1.1/8.8.8.8 на WDTT-WB **через туннель** (убран bypass на physical NIC).
- **MTU**: 1380 (как WG) — меньше фрагментации на WebRTC+KCP.
- **metric**: WDTT-WB interface metric=1 — Windows предпочитает VPN-адаптер.

## [0.3.85] — 2026-06-29

### WB Stream — reconnect после disconnect не стартует
- **awaitShutdown**: 5 s + принудительный `EmergencyDown` (маршруты/DNS WDTT-WB).
- **runner**: быстрый shutdown (session 2s, join-loop 2s, tun 4s).
- **runGen**: stale runner не сбрасывает UI после force-reconnect.

## [0.3.84] — 2026-06-29

### WB Stream — «туннель уже запущен» после отключения
- **Disconnect**: UI отключается мгновенно; cleanup в фоне.
- **Connect**: ждёт завершения предыдущего runner (`awaitShutdown`) перед новым подключением.

## [0.3.83] — 2026-06-29

### WB Stream — фикс 30 с «мёртвого» интернета после подключения
- **DNS**: netstack-путь не ставит 1.1.1.1/8.8.8.8 на WDTT-WB — Windows использует роутер (192.168.8.1), как VK.
- **bypass**: public DNS (1.1.1.1/8.8.8.8) на physical NIC **до** split-default маршрутов.
- **TCP**: dial timeout 30s → 12s (не ждём полминуты на зависших соединениях).
- **warmup**: OS probe с A-only lookup (без Windows AAAA flake).

## [0.3.82] — 2026-06-29

### WB Stream — фикс зависания «Отключение…» (reconnect при shutdown)
- **wbjrunner**: при Disconnect не эмитится `TUNNEL_RECONNECTING` и не ретраится join-loop.
- **session**: `Close()` гарантированно закрывает `Done` (не ждёт 15s ICE drain на сервере).
- **UI**: после `DisconnectWB` принудительно `idle` в `finally`.


### WB Stream — VPN как VK (DNS + маршруты)
- **DNS**: не перехватываем DNS на WDTT-WB — система использует роутер (192.168.8.1), как WG.
- **bypass**: signaling + 1.1.1.1/8.8.8.8 всегда через физический NIC (аналог VK TURN exclude).
- **warmup**: OS probe через tcp4; убран `releaseResolverBypass` (ломал Windows DNS).

## [0.3.80] — 2026-06-28

### WB Stream — фикс ↓0 B / UI «Подключение…» при reconnect
- **server creator**: `RestartLink` когда joiner возвращается (sub offer skip + stale smux).
- **warmup**: при таймауте probe UI переходит в running + WARN (не вечное «Подключение…»).

## [0.3.79] — 2026-06-28

### WB Stream — фикс 403 «guests cannot create rooms»
- **server**: PingLoop при broken pipe закрывает WS → creator переподключается, не зависает.
- **UI**: понятная ошибка если creator на сервере не вещает.

## [0.3.78] — 2026-06-28

### WB Stream — фикс зависания «Отключение…»
- **shutdown**: joiner закрывается до `tun.Stop()` — gVisor не ждёт 30s на активных TCP.
- **netstack**: таймаут 4s на `st.Wait()` при остановке TUN.
- **session**: таймаут 5s на `sess.Close()` (DTLS).
- **UI**: `state_changed stopped` сразу при Disconnect, не после полной очистки.

## [0.3.77] — 2026-06-28

### WB Stream — фикс Windows DNS leak (Яндекс видит реальный IP)
- **bypass**: DNS 1.1.1.1/8.8.8.8 не bypass'ятся постоянно — только на окно reconnect-auth; после `TRAFFIC_READY` снимаются (Windows Smart DNS leak).
- **warmup**: второй probe через OS route (`[warmup] OS route ip=…`) — проверка что браузер идёт через туннель.

## [0.3.76] — 2026-06-28

### WB Stream — фикс reconnect после tunnel lost
- **session**: после `tunnel lost` снова вызывается `OnConnected` (WBT carrier rebind), не блокируется «skip re-fire».
- **joiner**: sub ICE после обрыва снова поднимает KCP, если peer переподключился.
- **server creator** (relay): при tunnel lost сбрасывается creator; при recovery — `SwapTunnel` + `RestartLink`.

## [0.3.75] — 2026-06-28

### WB Stream — bug-hunt: reconnect warmup + UI
- **warmup**: при reconnect после обрыва снова запускается ipify probe (`TRAFFIC_READY`), не только первый connect.
- **UI**: статус `connecting` при `TUNNEL_RECONNECTING` (joiner cleared).

## [0.3.74] — 2026-06-28

### WB Stream — фикс reconnect при активном TUN
- **reconnect**: перед `guest-register` обновляется bypass `stream.wb.ru` / `auth-stream.wb.ru` + DNS (1.1.1.1, 8.8.8.8) через физический NIC — auth не идёт в мёртвый туннель.
- **joiner**: сброс joiner при `session end` / `tunnel lost` (0.3.73).

## [0.3.73] — 2026-06-28

### WB Stream — фикс zombie после обрыва WebRTC
- **joiner**: при `tunnel lost` / переподключении WebRTC сбрасывается KCP+joiner и поднимается заново (раньше skip duplicate OnConnected → ↓ keepalive, Яндекс видит реальный IP).
- **WBT в стате**: показывает SRTT (реальный RTT), не RTO.

## [0.3.72] — 2026-06-28

### WB Stream — фикс egress (SOCKS noauth + KCP timing)
- **server**: пустой `upstream_user` не подставляет пароль — xray SOCKS `noauth` на 10879 снова работает (раньше `dial failed: rejected auth 0xff`, ↓ 0 B).
- **relay**: joiner WBT/KCP стартует после sub ICE + inbound VP8 (не на pub-only ICE).
- SOCKS-клиент при явном user/pass предлагает noauth и userpass — совместимость с mixed inbound.

## [0.3.71] — 2026-06-28

### WB Stream — фикс KCP до входящего VP8
- **joiner**: WBT/KCP стартует после первого VP8-фрейма от creator (sub track), не на pub-only ICE.
- Устраняет ↓ 0 B и timeout warmup ipify при «туннель подключён».

## [0.3.70] — 2026-06-28

### WB Stream — фикс «↓ 0 B», warmup ipify timeout
- **relay/session**: sub ICE/offer не вызывает повторный `OnConnected` в WBT-режиме — KCP не ломается через 1 с после connect.
- **joiner/creator**: игнор дублирующего `OnConnected` (раньше `SwapTunnel` убивал ответный трафик).

## [0.3.69] — 2026-06-28

### WB Stream — стабильность как VK (без обрывов страниц)
- **relay**: WebRTC rebind не убивает smux/KCP — браузерные TCP не рвутся при ICE recovery.
- **relay/creator**: DNS/UDP через xray SOCKS (как TCP), не direct с VPS.
- **desktoptun**: TCP dial 30s, UDP idle 120s, keepalive, 512 TCP relays.
- **panel**: пустой `upstream_user` → per-user xray auth (`$password$`).

## [0.3.68] — 2026-06-28

### WB Stream — видимый трафик в логах и tray
- **СТАТ каждые 3 с**: `[WB СТАТ] ↓ … ↑ … · WBT … ms` — как VK, видно что байты реально идут.
- **Tray**: счётчики rx/tx обновляются в WB-режиме.
- **relay**: netstack relay через `sessionstats.Copy`; сброс счётчиков при старте joiner.

## [0.3.67] — 2026-06-28

### WB Stream — фикс «туннель активен, трафик не идёт» после reconnect
- **relay/creator**: `SwapTunnel` пересоздаёт smux-server после reset KCP (раньше smux оставался на мёртвой сессии).
- **relay**: rebind на sub offer + sub ICE (reconnect без смены ICE state).
- **joiner**: peer epoch restart пересоздаёт KCP+smux.
- **UI**: «Подключено» только после `TRAFFIC_READY` (warmup ipify), не при поднятии TUN.
- Сервер + клиент пересобраны.

## [0.3.66] — 2026-06-28

### WB Stream — логи как VK + быстрее загрузка страниц
- **Логи**: фильтр `classifyWBLog` — убраны тысячи `[GO]` vp8/lk-video/signal/ping; только `[WB]` INFO/WARN/ERROR.
- **relay**: убраны per-frame логи в `vp8tunnel` и `session.readVP8Track`.
- **Скорость TUN**: VP8 fps/batch 30/48 (было 20/32) — ~2× полоса для HTTP, меньше «думает перед загрузкой».
- **UI**: строка подключения WB — INFO вместо GO.
- Пересобран `wdtt-windows-amd64.exe`.

## [0.3.65] — 2026-06-28

### WB Stream — in-process (как VK), без отдельного wbt-joiner
- **WDTT**: WB туннель работает внутри `wdtt-app` через `wbjrunner` — в Task Manager не появляется отдельный `wbt-joiner.exe`.
- **relay**: логика joiner вынесена в `wbjrunner` (CLI `wbt-joiner` — тонкая обёртка для E2E).
- Пересобран `wdtt-windows-amd64.exe`.

## [0.3.64] — 2026-06-28

### WB Stream — фикс утечки памяти (8 ГБ) + счётчики трафика + VK-style relay
- **Память**: убрана goroutine на каждый UDP-пакет; лимит 256 smux-потоков / 128 UDP-handler'ов; smux-буферы 4 МБ / 512 КБ.
- **VK-style netstack**: вместо глобального `tunnel.T()` + SOCKS — прямой relay wintun → smux (`directHandler`), как `vkwg` → `tnet.DialContext`.
- **VP8 в TUN-режиме**: fps capped 20, batch 32 — меньше CPU/RAM на полном VPN.
- **UI stats**: rx/tx в netstack-пути.
- Пересобран joiner (Win/Linux) + `wdtt-windows-amd64.exe`.

## [0.3.63] — 2026-06-28

### WB Stream — SOCKS полностью убран (netstack VPN как VK/WG)
- **Клиент**: `wbt-joiner` больше не поднимает локальный SOCKS5 (`127.0.0.1:1080`). Только `--tun` + in-process netstack → smux-туннель (аналог wireguard-go netstack у VK). Флаги `--socks-*` и `-tun-inproc` удалены.
- **PWDTT**: `WBManager` не передаёт socks-параметры; убраны `WBSocksAddr` и UI-строки про SOCKS5.
- **E2E**: `test-wbt-joiner-e2e.sh` проверяет `--tun` + `STATUS:TRAFFIC_READY` + рост STATS (без curl через SOCKS).
- Пересобран joiner (Win/Linux) + `wdtt-windows-amd64.exe`.

## [0.3.62] — 2026-06-28

### WB Stream — фикс зависания «Подключение…» / 2 ГБ RAM / SOCKS убран в TUN
- **Deadlock в `desktoptun.Start()`**: метод держал `t.mu` на всё время netsh/route и вызывал `AddBypassIP` (повторный lock) — TUN никогда доходил до `STATUS:TUN_ACTIVE`, UI застревал на «Подключение…». WebRTC bypass в это время тоже блокировался на mutex.
- **Broadcast/multicast UDP** (`10.99.0.255:137`, `10.99.0.255:27036` и т.д.) отбрасываются в диалере — каждый пакет раньше поднимал отдельный PacketConn + горутины → раздувание памяти до ~2 ГБ.
- **SOCKS loopback отключён в TUN+inproc** (по умолчанию): joiner не биндит `127.0.0.1:1080` и не пишет `wbt: SOCKS5 on …`; warmup идёт напрямую через netstack dialer (`warmupTunnel`). SOCKS остаётся только для фолбэка (`-tun-inproc=false` или TUN недоступен).
- **Тихие логи tun2socks**: уровень `silent` после `CreateStack` — нет флуда `[TCP]/[UDP]` на каждый флоу.
- Пересобран встроенный `wbt-joiner` (Windows/Linux) + Wails дистрибутивы.

## [0.3.61] — 2026-06-28

### WB Stream — LAN (RFC1918) больше не заходит в туннель + тихие логи netstack
- **Корень проблемы «у VK тихо, у WB шторм».** Оба фронтенда захватывают весь трафик устройства одинаковыми split-default маршрутами (`0.0.0.0/1` + `128.0.0.0/1`). Но VK (wireguard-go) инкапсулирует всё в **один** UDP-поток, а WB (tun2socks netstack) поднимает **отдельный обработчик/диалер на каждый 5-tuple**. Приложение в LAN, долбящее, например, `10.54.217.44:161` (SNMP-сканер/монитор), превращалось в тысячи netstack-флоу/сек — хотя диалер в итоге дозванивался локально (`IsNonRoutableHost`), пакет уже зашёл в wintun.
- **Фикс: LAN (RFC1918) исключён из туннеля на уровне маршрутов** (`10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`) — bypass через оригинальный шлюз ставится в `desktoptun.Start()` ДО split-default (Windows: `route ADD … METRIC 1`; Linux: `ip route add … via gw`). Теперь LAN-трафик вообще не доходит до netstack — поведение «allow LAN», как у обычных VPN, и туннель так же тих, как VK. Своя подсеть TUN сохраняет более специфичный on-link маршрут и не задета. Маршруты снимаются в `Stop()`.
- **Тихие логи tun2socks.** In-process путь (v0.3.60) не понижал уровень логгера tun2socks, поэтому каждый TCP/UDP-флоу писался на `info` (`tunnel/udp.go:47`) — при всплеске это тысячи строк/сек + лишний CPU. Теперь `quietTun2socksLogs()` ставит `warn` для обоих путей (in-process и SOCKS-fallback).
- **Проверка на сервере**: pre-release gate (unit + live E2E против WB-creator) — PASS (tunnel 1с, ttf 0с, throughput 26.5 Mbps, stress PASS, exit `178.173.248.141`). Сервис `wdtt` активен, VK DTLS-хендшейки проходят, штормов/exhaust в логах нет.
- Пересобран встроенный `wbt-joiner` (Windows/Linux).

## [0.3.60] — 2026-06-28

### WB Stream — убран локальный SOCKS из полного VPN (единый netstack-фронтенд)
- **Нет больше петли `tun2socks → 127.0.0.1:1080 → joiner`.** В режиме `--tun` netstack tun2socks теперь дёргает диалер **внутри процесса** (`tunnel.SetDialer` + `core.CreateStack`): пакеты из wintun сразу превращаются в smux-стримы WB-туннеля. Это устраняет сам класс багов «SOCKS port exhaustion» / шторма соединений к sink-адресам (например `172.31.255.254`), а не лечит симптом.
- **Единый принцип фронтенда с VK**: VK поднимает wireguard-go netstack, WB теперь тоже работает через in-process netstack (gvisor) вместо локального SOCKS-сервера. Транспорт WB остаётся прежним (KCP+smux поверх VP8 до creator'а — он по-прежнему терминирует соединения как прокси, отдельный WG-bridge на сервере не требуется).
- **Диалер**: новые `Joiner.DialTCP` (smux-stream + отсечение sink/fake-dns/локальных адресов) и `Joiner.DialUDP` (`net.PacketConn` поверх пула UDP-стримов, для DNS).
- **Фолбэк сохранён**: флаг `-tun-inproc=false` возвращает старый путь `tun2socks → SOCKS5`; SOCKS-сервер по-прежнему доступен для ручного режима без `--tun`.
- **Тесты**: добавлены unit-тесты `TestWBTDialTCP`, `TestWBTDialTCPRejectsSink`, `TestWBTDialUDP` (round-trip через in-memory туннель creator↔joiner). Pre-release gate (unit + live SOCKS E2E) — PASS.
- Пересобран встроенный `wbt-joiner` (Windows/Linux).

## [0.3.59] — 2026-06-28

### WB Stream — шторм `172.31.255.254` / exhaustion SOCKS
- **Отклонение tunnel-sink адресов** (`172.31.255.254` wintun gateway, `10.99.0.x`) — joiner больше не делает `local dial` на сотни соединений; возвращает SOCKS fail.
- **Bypass `172.31.255.254`** на физическом NIC при поднятии TUN — tun2socks не гоняет этот трафик в SOCKS.
- Пересобран встроенный `wbt-joiner` (Windows/Linux).

## [0.3.58] — 2026-06-28

### WB Stream — «поднят, но трафика нет» и обрывы при speedtest
- **UI «Подключено» только после TUN ACTIVE**, а не сразу после WebRTC — раньше статус «активен» показывался до поднятия wintun/маршрутов, и 1–3 мин казались «мёртвыми».
- **VP8 pacing 30/64** (как у creator) вместо 24/30 — выше пропускная способность, меньше затыков при параллельных загрузках.
- **Warmup после TUN:** joiner сам прогоняет HTTP через SOCKS и пишет `STATUS:TRAFFIC_READY` — KCP/DNS прогреты до первого клика пользователя.
- **smux буферы увеличены** (2 MB stream / 16 MB session) — параллельные TCP (speedtest) не вешают туннель.
- **Headless E2E-тест** `test-before-release.sh` на сервере: unit + live joiner против creator, замер tunnel/ttf/throughput/stress.
- **stdin EOF** больше не убивает joiner в headless/daemon (только pipe от pwdtt).
- Пересобран встроенный `wbt-joiner` (Windows/Linux).

## [0.3.57] — 2026-06-28

### WB Stream — один shared SOCKS UDP relay
- **Убран шторм `SOCKS UDP ASSOCIATE`.** tun2socks открывает ASSOCIATE на каждый UDP-поток; joiner раньше поднимал отдельный `ListenUDP` на каждый — сотни строк в логе. Теперь **один shared relay** на `127.0.0.1` для всех ASSOCIATE; smux-пул (v0.3.56) продолжает держать 1 stream на `host:port`.
- Пересобран встроенный `wbt-joiner` (Windows/Linux).

## [0.3.56] — 2026-06-28

### WB Stream — DNS/UDP без шторма smux-стримов
- **Пул UDP поверх WBT.** Раньше каждый DNS-запрос (и любой UDP datagram) открывал новый smux stream → лавина `SOCKS UDP ASSOCIATE` и лишний VP8 трафик. Теперь joiner держит **один smux stream на пару host:port** (1.1.1.1:53, 8.8.8.8:53) и шлёт последующие datagram'ы по нему; creator мультиплексирует ответы в том же stream.
- Idle-потоки закрываются через 45 с без запросов.
- Пересобран встроенный `wbt-joiner` (Windows/Linux).

## [0.3.55] — 2026-06-28

### WB Stream — фикс обрыва TUN через ~5 с (routing loop)
- **TURN/STUN больше не уходит в свой же туннель.** После поднятия wintun WebRTC-трафик к `wb-stream-turn-1.wb.ru` (185.62.200.94:3478/5349) и SFU-реле шёл через SOCKS→WBT вместо физического NIC — ICE деградировал, сервер слал `PARTICIPANT_REMOVED`, WebRTC закрывался и начинался цикл переподключений.
- Добавлен bypass для TURN (`wb-stream-turn-1.wb.ru`), RTC-хоста из сессии и всех ICE-server hostname из join-ответа; SFU-кандидаты из SDP парсятся построчно.
- **Host-кандидат с IP туннеля (10.99.0.x) больше не публикуется** — Pion фильтрует адреса подсети wintun.
- Пересобран встроенный `wbt-joiner` (Windows/Linux).

## [0.3.54] — 2026-06-28

### WB Stream — фикс «обрывистости» (рывки при сёрфинге)
- **Входящие KCP-сегменты больше не теряются молча.** В несущей WBT (KCP поверх VP8) исходящий путь имел backpressure, а **входящий при переполнении буфера молча отбрасывал сегменты**. KCP — надёжный транспорт, поэтому такой дроп оборачивался дорогими RTO-ретрансмитами: задержки скакали, DNS-запросы (1.1.1.1/8.8.8.8) уходили в таймаут и **ретраились по нескольку раз** (видно в логах: один и тот же порт долбит резолверы 5–7 секунд), страницы грузились рывками.
- Теперь при всплеске (DNS-шторм + видео + TCP) доставка делает короткий backpressure вместо дропа (предохранитель 2 c против зависшего потребителя) — KCP остаётся надёжным, сёрфинг ровнее.
- Пересобран встроенный `wbt-joiner` (Windows/Linux) с этим фиксом.

## [0.3.53] — 2026-06-25

### WB Stream — DNS/UDP через TUN, быстрее «оживает» интернет
- **SOCKS5 UDP ASSOCIATE.** tun2socks гонял DNS (1.1.1.1:53, 8.8.8.8:53) через SOCKS, а joiner принимал только TCP CONNECT — отсюда сотни `UDP ASSOCIATE: command not supported` и задержка, пока приложения не перейдут на TCP/кэш DNS. Теперь UDP (DNS и прочее) туннелируется через WBT на creator.
- После `TUNNEL CONNECTED` интернет через полный VPN должен подниматься заметно быстрее; TCP как и раньше работал, тормозил именно UDP/DNS.

## [0.3.52] — 2026-06-25

### Сборка и релиз — готовый `.exe` через CI
- **Теперь есть скачиваемый релиз.** Добавлен GitHub Actions workflow (`Build Windows`), который собирает `wdtt.exe` на Windows-раннере (Go 1.26 + Wails v2.12.0 + Vite) и публикует его в Releases при каждом теге `v*`. Раньше релиз был только git-тегом без бинарника, поэтому на ПК оставалась старая версия без новых функций.
- Все функции, уже присутствующие в исходниках (копирование логов, очистка логов, поиск/фильтры, настройки как в веб-превью), попадают в опубликованный бинарник — достаточно скачать свежий `.exe` из Releases.

## [0.3.51] — 2026-06-24

### WB Stream — авто-выбор свободного SOCKS-порта
- **Больше не «порт занят».** Если порт SOCKS5 (1080) занят залипшим процессом от прошлой сессии, joiner раньше отказывался поднимать туннель (`STATUS:SOCKS_BIND_FAILED`) и WB не подключался, пока не перезапустишь приложение. Теперь SOCKS биндится **до** старта с авто-фолбэком: предпочитаем 1080, а если занят — берём любой свободный порт от ОС. SOCKS внутренний (его слушает tun2socks внутри joiner-а), поэтому номер порта не важен.
- Фактический порт прокидывается в UI/логи (`STATUS:SOCKS_PORT:<n>`): «SOCKS5 порт занят — joiner использует свободный N».
- Это устраняет ситуацию «отключило и нельзя подключиться заново»: повторный коннект теперь поднимается на свободном порту, не конфликтуя со старым процессом.

## [0.3.50] — 2026-06-24

### WB Stream — фикс блэкаута трафика и чистое отключение TUN
- **Исправлен полный блэкаут интернета при TUN.** Если SOCKS5-порт занят (например, залип прошлый процесс), joiner раньше всё равно поднимал TUN и заворачивал весь трафик в нерабочий SOCKS → лавина `client handshake: EOF` и интернет пропадал. Теперь SOCKS биндится **синхронно**, и TUN поднимается **только при успешном бинде**. Если порт занят — ошибка в логах, TUN не трогаем.
- **Чистое отключение (graceful shutdown).** При «Отключить»/выходе приложение просит joiner корректно завершиться (через stdin) — тот **снимает TUN-адаптер и default-маршруты**. Раньше жёсткий kill оставлял маршруты, и после отключения интернет оставался сломанным (особенно на Windows). Принудительный kill — только если за 4 c не завершился сам.
- Игнорируем мусорный `0.0.0.0` среди ICE-кандидатов при установке bypass-маршрутов.

> Если интернет «завис» после старой сборки (0.3.48/0.3.49) — перезагрузка очистит залипшие маршруты wintun.

## [0.3.49] — 2026-06-24

### WB Stream — фикс регресса TUN (wintun.dll) + защита от падения
- **Исправлено падение WB на Windows.** В 0.3.48 при включённом TUN `wbt-joiner` падал с `wintun.dll could not be found` (tun2socks `engine.Start` делает `fatal` → процесс умирал, WB отваливался целиком). Теперь `wintun.dll` распаковывается рядом с joiner-ом (тот же DLL, что и для VK WireGuard).
- **Fail-safe для TUN.** Перед поднятием TUN joiner проверяет доступность (Windows: загрузка `wintun.dll`; Linux: root). Если недоступно — **остаётся рабочий SOCKS5**, процесс больше не падает. В логах: «TUN недоступен — работает только SOCKS5».
- Убрано двойное событие статуса «Туннель активен» (обрабатываем только машинную строку `STATUS:`).

## [0.3.48] — 2026-06-24

### WB Stream — полный VPN (TUN), без отдельного окна
- **Полный VPN для WB.** Теперь WB поднимает системный TUN-адаптер (`WDTT-WB`, wintun на Windows) и заворачивает **весь трафик устройства** через WB Stream (tun2socks поверх локального SOCKS5), а не только приложения с прокси. Маршруты сигналинга/SFU/TURN (`stream.wb.ru`, `rtc-el-01.wb.ru` и ICE-кандидаты) идут в обход туннеля, чтобы сам WebRTC-канал не зацикливался.
- **Убрано чёрное окно консоли.** Встроенный `wbt-joiner` запускается скрыто (Windows: GUI-подсистема + `CREATE_NO_WINDOW`/`HideWindow`) — никаких всплывающих окон, движок работает внутри приложения.
- Новый статус в логах: «Полный VPN активен — весь трафик через WB Stream».
- Требуются права администратора (как и для VK WireGuard) — TUN-адаптер создаётся системно.

## [0.3.47] — 2026-06-24

### WB Stream — рабочее подключение
- **WB теперь реально подключается** (не заглушка). При выборе протокола **WB** кнопка «Подключить» поднимает WBT-туннель (KCP+smux поверх VP8) к комнате из подписки (`wb_room`) и открывает локальный **SOCKS5 на 127.0.0.1:1080**.
- Встроенный `wbt-joiner` (тот же WBT-путь, что и в iOS-приложении) — совместим с in-process WB Creator панели WDTT. Бинарь вшит в клиент (Linux + Windows), распаковывается в `~/.config/pwdtt/bin` / `%AppData%`.
- **Метрики WB:** WBT (RTT туннеля, мс) · VP8 (кадров/с) · RTT (end-to-end). Трафик rx/tx — из активной сессии.
- Логи подключения WB в реальном времени (GO/STATUS/WARN).

## [0.3.46] — 2026-06-20

### UI
- **Выбор протокола VK / WB** на главном экране подключения (сегмент сверху).
- **Настройки по протоколу:** VK → cookies, хеши, MTU; WB → VP8, proxy, dual-track.
- **Мобильный UI:** без «Трей» и «Запускать при старте» (только десктоп Wails).
- **Хеши из подписки:** профильные хеши используются даже при пустых глобальных; кнопка «VK Хеши профиля» в настройках.
- **Строгое разделение VK/WB:** настройки, добавление и редактирование профиля зависят от выбранного протокола.
- **VK:** метрики TURN · DTLS · Интернет, подпись «VK Calls · TURN» — полная поддержка как в v0.3.45.
- **WB:** визуал WB Stream (WBT · VP8 · RTT); подключение — в следующей версии.

## [0.3.45] — 2026-06-24

### Исправлено
- **VK auth регрессия v0.3.41–v0.3.44:** откат `auth.anonymLogin` на okcdn к **v0.3.40** (`session_data` version **2**, без `auth_token`). v0.3.41 ломала step4 ошибкой `Access token is broken`.
- **Toggle OFF:** сразу VK Calls → legacy (как v0.3.40), cookies с диска не подхватываются автоматически.
- **Toggle ON:** только cookie-path (opt-in).
- Live gate: `scripts/test-live-vk.sh` + `go test -tags=live -run TestLiveVKCreds`.

## [0.3.44] — 2026-06-24

### Исправлено
- **VK auth:** по умолчанию снова **анонимный вход** (как v0.3.41 без cookies). Тумблер «VK cookies» — явный opt-in: включил → только cookie-path, выключил → anonymous (VK Calls + legacy), файл cookies на диске не подхватывается сам.
- **Тумблер в UI:** исправлен race — состояние берётся из `GetVKUseCookies`, не перезатирается статусом cookies.
- Убран fail-fast «Access token is broken» на anonymous — снова fallback на legacy как в v0.3.41.

## [0.3.43] — 2026-06-24

### Исправлено
- **VK auth регрессия v0.3.42:** если `cookies-vk.json` есть, cookie-путь снова включён по умолчанию (как v0.3.41). Toggle «VK cookies» только для явного opt-out.
- **Fail-fast:** при `participant.check.flood` и `Access token is broken` клиент больше не гоняет anonymous/legacy/captcha — сразу понятная ошибка (новый hash + пауза / нужны cookies).
- Сохранение cookies автоматически включает cookie-auth.

## [0.3.42] — 2026-06-24

### VK auth
- **Переключатель «VK cookies»** в настройках: выключено — анонимный вход (VK Calls + legacy, как раньше); включено — сначала remixsid из cookies, затем fallback на анонимные пути.
- Настройка сохраняется в `~/.config/pwdtt/settings/vk-auth.json`.

## [0.3.41] — 2026-06-24

### VK (анонимный вход закрыт)
- **Вход через cookies (remixsid).** VK больше не принимает анонимный join (`error.webrtc.auth.anonym_token.not_found`). Клиент пробует cookie-путь первым: `web_token` → `getCallToken` → join без anonymToken.
- **Настройки → VK cookies** — вставка JSON/`remixsid=...`, сохранение в `~/.config/pwdtt/secrets/cookies-vk.json`.
- **Проверка срока cookies** — статус показывает «устарели», если `remixsid` есть, но `web_token` не выдаёт токен.
- **Fallback на legacy/VK Calls** сохранён, если cookies не заданы.

## [0.3.40] — 2026-06-20

### Стабильность (cred pool как в anton48/vk-turn-proxy-ios)
- **Пул call-credential на группу** вместо одного VK-креда на все 9 воркеров. Раньше при истечении call-credential (~60–90 c) умирала вся группа разом → волна реконнектов (как в логах: 4 воркера за 5 секунд). Теперь группа получает несколько независимых кредов (формула как `poolSizeForNumConns` в [anton48/vk-turn-proxy-ios](https://github.com/anton48/vk-turn-proxy-ios)): при 9 воркерах — 4 слота (~2–3 воркера на кред). Истечение одного креда убивает только его слот, остальные держат туннель.
- **Обновление кредов по слоту**, а не всей группы: `refreshCredSlot(slot)` инвалидирует только упавший credential-stream.
- **Медленный stagger** для воркеров 7–9 (3s между стартами, как `slowStagger` в эталоне) — меньше шторма TURN Allocate.
- **Фазовый сдвиг групп** увеличен 25s → 35s для лучшего десинхрона жизни кредов между группами.
- **consent-timeout** 30s → 90s (ближе к zombie-порогу 120s в эталоне) — меньше ложных убийств воркеров.

## [0.3.39] — 2026-06-20

### Изменено
- **Снят лимит воркеров.** Убран адаптивный кап 3/6/9 по числу relay-хостов: теперь всегда запускается полное запрошенное число воркеров независимо от того, сколько эндпоинтов выдал VK. (Шторм квоты 486, ради которого вводился кап, на практике вызывал серверный баг write-deadline — исправлен в server v1.4.51.)
- **«VK через туннель» — всегда включено нативно.** Тумблер убран из настроек. VK веб/API (`login.vk.com`, лента) всегда идут через VPN-сервер; TURN-транспорт туннеля — всегда напрямую. Backend форсит режим при подключении.

## [0.3.38] — 2026-06-20

### Стабильность (туннель не прерывается при смерти relay — как в эталонном vk-turn-proxy)
- **Общая очередь отправки вместо пуша в канал конкретного воркера.** Сверил, как держит соединение эталонный [`anton48/vk-turn-proxy-ios`](https://github.com/anton48/vk-turn-proxy-ios): там все соединения читают из **одного общего `sendCh`** (work-stealing) — свободное соединение забирает следующий пакет, а смерть одного relay просто означает, что это соединение перестаёт качать; остальные продолжают из той же очереди → ни одного потерянного пакета, ни паузы.
  - У нас было иначе: диспетчер **пушил пачку (chunk) пакетов в `SendCh` конкретного воркера** по round-robin. Когда VK гасил relay этого воркера, его недоотправленные пакеты **терялись**, и до перевыбора указателя возникал микро-обрыв. Отсюда ощущение «связь проседает / теряется в игре» при штатном 16-секундном рецикле relay.
  - Теперь диспетчер кладёт пакеты в **общую `d.SendCh`**, а все воркеры конкурентно читают из неё. Смерть воркера невидима для туннеля: соседи мгновенно подхватывают поток. Это ровно модель эталона.
  - Убрана chunk-affinity / round-robin логика и per-worker `SendCh`. Порядок пакетов между воркерами больше не гарантируется, но WireGuard устойчив к переупорядочиванию (anti-replay window) — эталон полагается на то же.
  - Буфер общей очереди `sendChBuf=1024`: при кратковременной просадке числа живых воркеров пакеты копятся, а не дропаются.

## [0.3.37] — 2026-06-20

### Стабильность (сглаживание агрегата, меньше провалов в игре)
- **Фазовый сдвиг групп.** VK гасит все TURN-аллокации под одним call-credential пачкой при его истечении (~50–60 c). Раньше обе группы стартовали почти одновременно → их креды старели синхронно и умирали вместе → число активных воркеров проваливалось до 1–3 (рывок/лаг в игре). Теперь каждая следующая группа стартует со сдвигом 25 c, поэтому пока одна группа пересоздаётся, другая держит туннель — агрегат не проседает.
- **Постоянная фаза воркера.** Внутри группы у каждого воркера свой случайный сдвиг переподключения, чтобы 16-секундные рециклы VK не сходились в синхронную волну.

> Важно: сам факт, что VK перерабатывает relay (~16 c) и гасит креды (~60 c) — это сторона VK, отключить нельзя (тот же pion-refresh, что и в эталонных реализациях, уже работает). Эти правки делают так, чтобы рециклинг VK **не чувствовался** на туннеле.

## [0.3.36] — 2026-06-20

### Исправлено
- **Воркер больше не долбится в мёртвый VK-хост, который не отвечает на Allocate** (`TURN Allocate: all retransmissions failed`). Раньше провал `Allocate` происходил до подключения учёта relay-health, поэтому мёртвый хост не штрафовался и `pickHealthyTurnURL` продолжал его выбирать (в логах один воркер ловил «all retransmissions failed» по 5–6 раз подряд). Теперь здоровье хоста фиксируется **всегда** — в т.ч. при провале Allocate/DTLS до READY, — и picker уводит воркеров с такого хоста.
- **При повторном «нет ответа на Allocate» обновляются креды.** Если все TURN-хосты в группе мёртвые, после нескольких попыток клиент запрашивает у VK свежий набор хостов (с защитой от частых запросов — не чаще раза в 15 c), вместо того чтобы крутить тот же мёртвый набор.

## [0.3.35] — 2026-06-20

### Изменено
- **Рецикл TURN-relay со стороны VK больше не помечается как ошибка.** VK периодически закрывает TURN-allocation (~16 c, поведение call-кредов) — relay отдаёт чистый `EOF`, и воркер тут же переподключается на свежий relay. Раньше это логировалось как `[ВОРКЕР] Ошибка Reader … EOF` и красилось в красный `[ERROR]`, пугая, хотя туннель работает штатно (трафик не прерывается). Теперь штатное закрытие relay (EOF/closed) после READY логируется спокойным INFO «переподключение (VK обновил relay)». `[ERROR]` остаётся только для настоящих сбоев: таймауты DTLS-хендшейка, auth, квота, не-EOF ошибки чтения.

## [0.3.34] — 2026-06-20

### Исправлено
- **Шторм TURN-аллокаций (error 486) при малом числе relay-эндпоинтов.** Если VK выдаёт мало relay-хостов (1–2), а на них наваливается полная группа из 9 воркеров, упираемся в квоту одновременных TURN-аллокаций → VK «жмёт» relay через ~16 с → массовый `EOF`, переаллокация, `Allocation Quota Reached`, воркеры уходят в ожидание `ждём ~58s`. Самоподдерживающийся цикл.
  - Добавлен **адаптивный лимит воркеров на группу** по числу различных relay-хостов в кредах: 1 хост → 3 воркера, 2 хоста → 6, ≥3 → полные 9. При неизвестном числе (0) не зажимаем.
  - Тайминги (stagger 1.2 с, старт группы #2 через 2 с, sem=4, backoff 60 с на 486) без изменений.

## [0.3.33] — 2026-06-20

### Исправлено
- **Переключение между серверами**: профиль на диске теперь ключуется по уникальному `id` сервера, а не по отображаемому имени. Раньше два сервера с одинаковым именем (частый случай при импорте из одной панели) писались в один файл `<name>.json` → `loadProfile(name)` всегда отдавал один и тот же `PeerAddr`, поэтому после подключения к одному серверу попытка переключиться на другой снова уводила на первый («как будто переключения нет»).
  - Connect передаёт `profile = server.id`; отображаемое имя идёт отдельным полем `name` только для имени лог-файла.
  - Add/Edit/импорт по ссылке/удаление сохраняют и удаляют профиль по `id`.
  - Одноразовая миграция: существующие профили переписываются по `id` из данных `serverStore` (они верны для каждого сервера), `device_id` берётся из старого профиля по имени.

## [0.3.32] — 2026-06-20

### Исправлено
- **Откат агрессивных таймингов 0.3.31**, которые вызвали нестабильность: stagger воркеров 450 ms и старт группы #2 через 500 ms утраивали частоту TURN Allocate → квота VK (`error 486`) → relay убивались за ~16 s вместо 90–100 s. Вернул stagger 1.2 s и фолбэк группы #2 на 2 s.
- **Сохранено полезное из 0.3.31**: WireGuard-конфиг по-прежнему может запросить любой воркер группы #1 (быстрый старт wg-turn). Группа #2 теперь стартует сразу после доставки wg_config, иначе фолбэк 2 s.

## [0.3.31] — 2026-06-19

### Исправлено
- **Быстрый старт трафика**: wg_config может запросить любой воркер группы #1 (не только первый) — если #1 завис на GETCONF, #2/#3 подхватят. Группа #2 стартует после GETCONF или через 500 ms (было 2 s). Stagger воркеров 450 ms вместо 1.2 s.

## [0.3.30] — 2026-06-19

### Исправлено
- **Consent-freshness**: upload-only воркеры больше не убиваются ложно — путь «мёртв» только если нет и входящих, и исходящих ~30 с.
- **WRAP_AUTH_TIMEOUT** — текст «Мёртвый TURN relay (таймаут DTLS)», не «Сервер не подтвердил пароль».
- **«VK через туннель»** — сохранение сразу при переключении, подсказка в настройках, режим не сбрасывается после auto-reconnect.

### Сервер
- Для consent-freshness нужен **wdtt-server с echo pong** на DTLS keepalive (`0xFF` → `0xFF`). Задеплоить unified `wdtt-app` с этим патчем.

## [0.3.29] — 2026-06-19

### Изменено
- **Consent-freshness на TURN-воркерах** (идея из WebRTC): отслеживаем входящую активность по DTLS (данные или pong на keepalive). Нет ответа дольше 30 c → путь считается «чёрной дырой», воркер убивается и пересоздаётся на здоровом relay (по RTT-скорингу). Раньше зомби-воркер (пакеты уходят, ответа нет, сокет без ошибки) жил до 30 минут, а диспетчер впустую лил в него трафик.
- Keepalive учащён 15 c → 10 c (быстрее детект мёртвого пути, безопаснее для NAT).

## [0.3.28] — 2026-06-19

### Добавлено
- **Мгновенная реакция на смену сети** (Wi-Fi↔LTE, новый Wi-Fi, смена шлюза): слушатель сети ОС (netlink на Linux, `NotifyAddrChange` на Windows) при реальной смене апстрим-шлюза сразу делает полный reconnect — раньше ждали залипания трафика (8 c) или 2× probe. Идея из VK (Cronet NetworkChangeNotifier). События от самого `wg-turn` игнорируются (шлюз не меняется).

### Сервер (installer v1.4.50)
- **BBR + fq qdisc** в `setup_sysctl`: выше throughput и стабильнее латентность под потерями на «замаскированном» пути (как QUIC/BBR2 у VK). Авто-`modprobe tcp_bbr`, если модуль не активен.

## [0.3.27] — 2026-06-19

### Добавлено
- **«VK через туннель»** (Настройки): переключатель гонит веб/API/CDN ВКонтакте через VPN (по умолчанию VK идёт напрямую — быстрый vk.com). Применяется на лету, без переподключения.
- TURN/WebRTC-подсети VK (транспорт самого туннеля) **всегда остаются напрямую** — переключатель их не трогает, чтобы не было петли маршрутизации.

## [0.3.26] — 2026-06-19

### Исправлено
- **Добавление по ссылке `wdtt://` без интернета**: все параметры (`ip`, `dtls`, `pass`, `hash`, `did`) берутся прямо из base64 ссылки. Раньше клиент игнорировал их и всегда лез на панель за `sub` → «Не удалось загрузить подписку» при недоступной панели/без сети.
- Панель теперь опциональна: если в ссылке есть `sub` и сеть доступна — подтягивается статистика; нет — сервер всё равно добавляется.

## [0.3.25] — 2026-06-16

### Изменено
- **Soft-reconnect**: авто-восстановление перезапускает только TURN/core, **wg-turn не сносится** — быстрее для игр, без полного «Отключено → VK auth → 18 воркеров».
- Probe `1.1.1.1:443` **не рвёт сессию**, пока жив хотя бы один воркер (ложное срабатывание при 18 активных).

## [0.3.24] — 2026-06-16

### Исправлено
- **↑ Отправлено**: считаются только пакеты, реально ушедшие в воркер; дропы при перегрузке каналов больше не завышают статистику.

## [0.3.23] — 2026-06-16

### Исправлено
- **VK в браузере (скелетон ленты)**: расширены split-маршруты — `93.186.224.0/19` (queuev4/login/id `.237.x`) и `95.142.192.0/19` (CDN `st*`). Раньше HTML шёл напрямую, а API ленты — через TURN.
- `vk_dns.go`: статические IP для `queuev4`, `eh.vk.com`, `st4-9.vk.com`.

## [0.3.22] — 2026-06-18

### Добавлено
- **Быстрое авто-переподключение** для игр: залипание трафика **8 с** → reconnect; потеря интернета (**2×** probe по 1.5 с) → reconnect; **0 воркеров 4 с** → reconnect. Проверка каждую **1 с**.

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
