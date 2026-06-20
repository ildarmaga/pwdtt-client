# Благодарности и происхождение кода

## [PWDTT](https://github.com/luminescq/PWDTT) — оригинальный десктопный клиент

**Автор:** [luminescq](https://github.com/luminescq)

Этот репозиторий (**ildarmaga/pwdtt-client**) — **модифицированная версия** проекта **PWDTT** (Copyright © 2026 luminescq), распространяемая на условиях **GNU GPL v3.0**.

Исходный проект: https://github.com/luminescq/PWDTT

### Что изменено относительно upstream PWDTT

- Интеграция с сервером WDTT (`wdtt://`, панель, профили по id)
- Улучшения стабильности TURN/DTLS (воркеры, relay-health, общая очередь отправки)
- VK через туннель по умолчанию, офлайн-импорт ссылок, UI/настройки

## [proxy-turn-vk-android](https://github.com/amurcanov/proxy-turn-vk-android)

**Автор:** [amurcanov](https://github.com/amurcanov)

Общий протокол VPN через VK TURN/DTLS и WireGuard.

## Другие проекты

| Проект | Использование |
|--------|----------------|
| [Wails](https://wails.io) | Desktop UI framework |
| [pion/dtls](https://github.com/pion/dtls), [pion/turn](https://github.com/pion/turn) | DTLS/TURN транспорт |
| [WireGuard](https://www.wireguard.com) | Туннелирование |

## Лицензия

GNU GPL v3.0 — см. [LICENSE](LICENSE).

При распространении бинарников необходимо предоставлять исходный код этого репозитория на условиях GPL v3.0.
