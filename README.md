# Закладки

Личный сервер закладок на Go: дерево папок, теги, карточка Open Graph, корзина, бэкапы и проверка ссылок. Расширения Chrome и Firefox добавляют текущую вкладку и умеют импортировать закладки браузера в отдельную папку.

Регистрации нет — один пароль владельца.

## Запуск

Нужен Go 1.24+. При первом старте задайте пароль:

```bash
export BOOKMARKS_PASSWORD='ваш-пароль'
go run ./cmd/bookmarks
```

Сервер слушает `http://127.0.0.1:8080`. Дальше пароль хранится как argon2id-хеш в SQLite (`DB_PATH`, по умолчанию `./data/bookmarks.sqlite`). Переменную можно не держать в окружении.

| Переменная | По умолчанию | Смысл |
|---|---|---|
| `LISTEN` | `127.0.0.1:8080` | адрес |
| `DB_PATH` | `./data/bookmarks.sqlite` | база |
| `BOOKMARKS_PASSWORD` | — | только первый запуск |
| `COOKIE_SECRET` | генерируется и пишется в БД | подпись cookie |
| `TRUSTED_PROXY` | `0` | доверять `X-Forwarded-For` |
| `BACKUP_DIR` | `./backups` | каталог бэкапов |

Сборка:

```bash
go build -o bin/bookmarks ./cmd/bookmarks
```

## Docker

На сервере нужны только `docker-compose.yml` и `.env`. Образ — scratch (статический бинарник + CA-сертификаты), публикуется CI в GHCR.

```bash
cp docker-compose.yml .env.example .  # или скачайте compose из репозитория
cp .env.example .env
# в .env задайте BOOKMARKS_PASSWORD (и по желанию COOKIE_SECRET)
mkdir -p data backups
docker compose pull
docker compose up -d
```

Кабинет: `http://127.0.0.1:30080`. Проброс наружу — на вашей стороне (Caddy, nginx, firewall).

SQLite лежит в `./data`, бэкапы — в `./backups` на хосте, не в контейнере.

Первый пуш CI создаёт пакет `ghcr.io/somnisom/bookmarks`. Если `docker compose pull` просит логин — в GitHub: Packages → bookmarks → Package settings → Change visibility → Public.

Локальная сборка образа: `docker build -t ghcr.io/somnisom/bookmarks:latest .`

## Кабинет

- `/` — дерево, поиск, теги, предпросмотр OG, заметки
- `/trash` — корзина
- `/settings` — расширения, пароль, JSON, бэкапы, проверка ссылок

Клавиатура в дереве: стрелки, Enter, Space, F2, Delete, `/`.

## Расширения

Не публикуются в магазины. Распакованная установка:

1. Соберите пакеты: `make extensions`
2. **Chrome / Chromium:** `chrome://extensions` → режим разработчика → «Загрузить распакованное» → `extensions/chrome`
3. **Firefox:** `about:debugging#/runtime/this-firefox` → «Загрузить временное дополнение» → `extensions/firefox/manifest.json`

В попапе укажите URL сервера (по умолчанию `http://127.0.0.1:8080`) и пароль. При первом запуске расширение создаёт `deviceId` в `storage.local`; если его стереть, сервер сбрасывает сессию.

- Хоткей (предлагается `Alt+Shift+D`) — сразу в последнюю папку
- Omnibox: в адресной строке `bm запрос`
- Импорт закладок браузера — новая корневая папка, без мержа
- В кабинете можно отключить требование входа для расширений (только для localhost)

## Данные

В git не попадают `.env`, `*.sqlite*`, `data/`, `backups/`. JSON-экспорт из настроек не содержит сессий.
