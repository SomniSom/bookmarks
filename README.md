# Закладки

Личный сервер закладок на Go: дерево папок, теги, карточка Open Graph, корзина, бэкапы и проверка ссылок. Расширения Chrome и Firefox добавляют текущую вкладку и умеют импортировать закладки браузера в отдельную папку.

Регистрации нет — один пароль владельца.

## Запуск

Нужен Go 1.27.3+ (версия зафиксирована в `go.mod`, старый тулчейн переключится сам). При первом старте задайте пароль:

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
| `BLOCK_PRIVATE_URLS` | `0` | запретить проверке ссылок и OG-фетчу ходить в localhost и приватные сети |

Сборка и проверки (всё через `make`):

```bash
make build   # bin/bookmarks, статический бинарник -trimpath -ldflags="-s -w"
make test    # go test -race ./...
make cover   # покрытие, итог в консоли
make lint    # golangci-lint run ./...
make vuln    # govulncheck ./...
make fix     # go fix + gofmt -w
```

CI (`.github/workflows/image.yml`) прогоняет gofmt, `go vet`, `go fix -diff`, тесты с `-race`, `golangci-lint`, `govulncheck` и только после этого собирает образ.

Линтер настроен в `.golangci.yml` (errcheck, staticcheck, revive, noctx, sqlclosecheck, rowserrcheck, modernize и др.). Покрытие тестами — около 79% по `internal/...`.

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

## Обновление на сервере

Прод — Docker Compose в `/opt/bookmarks`: образ берётся из `BOOKMARKS_IMAGE` (по умолчанию
`ghcr.io/somnisom/bookmarks:latest`), данные в `./data`, бэкапы в `./backups`.

```bash
# 1. Архивный бэкап (контейнер остановить на пару секунд, чтобы SQLite был консистентен)
TS=$(date -u +%Y%m%dT%H%M%SZ)
docker compose stop bookmarks
tar czf /opt/backups-manual/bookmarks-preupdate-$TS.tar.gz -C /opt bookmarks
docker compose start bookmarks

# 2. Сохранить текущий образ для отката
docker tag ghcr.io/somnisom/bookmarks:latest bookmarks:prev-$TS

# 3. Собрать новую версию на сервере (BuildKit не требуется)
docker build -t bookmarks:$TS .

# 4. Проверить на копии базы, прежде чем переключать
mkdir -p /opt/bookmarks-test/data
tar -xzf /opt/backups-manual/bookmarks-preupdate-$TS.tar.gz -C /opt/bookmarks-test \
  --strip-components=1 bookmarks/data/bookmarks.sqlite
docker run -d --name bm-test -p 127.0.0.1:30081:8080 -v /opt/bookmarks-test/data:/data \
  -e LISTEN=0.0.0.0:8080 -e DB_PATH=/data/bookmarks.sqlite --read-only --tmpfs /tmp \
  bookmarks:$TS
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:30081/login
docker rm -f bm-test && rm -rf /opt/bookmarks-test

# 5. Переключить и проверить
sed -i '/^BOOKMARKS_IMAGE=/d' .env
echo "BOOKMARKS_IMAGE=bookmarks:$TS" >> .env
docker compose up -d --pull never
docker logs bookmarks --tail 20
```

Откат — вернуть прежний тег и пересоздать контейнер:

```bash
docker compose down
sed -i '/^BOOKMARKS_IMAGE=/d' .env   # снова образ по умолчанию (GHCR)
docker compose up -d --pull never
```

## Расширение Chrome

`make extensions` пересобирает `extensions/chrome` из `extensions/shared`. Дальше
`chrome://extensions` → включить «Режим разработчика» → «Обновить» у расширения.

В настройках расширения укажите адрес сервера (`https://bookmarks.portfolio.com.ru`),
нажмите «Сохранить» и разрешите доступ к сайту в запросе Chrome, затем войдите по паролю.
Токен привязан к устройству (`X-Device-Id`) и лежит в `storage` расширения.

Сервер отвечает CORS только для origin `chrome-extension://…` и `moz-extension://…`;
для нового хоста доступ запрашивается заново. Загрузка unpacked из другой папки меняет
ID расширения и сбрасывает сохранённый токен.

## Кабинет

- `/` — дерево, поиск, теги, предпросмотр OG, заметки
- `/trash` — корзина
- `/settings` — расширения, пароль, JSON, бэкапы, проверка ссылок

Клавиатура в дереве: стрелки, Enter, Space, F2, Delete, `/`.

## Расширения

Не публикуются в магазины. Chrome — распакованная папка. Firefox обычного релиза ставит только **подписанный** xpi.

```bash
make extensions   # пакеты extensions/chrome и extensions/firefox
make xpi          # неподписанный dist/bookmarks.xpi (обычный Firefox его не поставит)
make xpi-sign     # подпись unlisted через AMO API, без витрины
```

**Chrome / Chromium:** `chrome://extensions` → режим разработчика → «Загрузить распакованное» → `extensions/chrome`

**Firefox на время сессии** (без подписи):

1. `make extensions`
2. `about:debugging#/runtime/this-firefox` → «Загрузить временное дополнение»
3. выбрать **`extensions/firefox/manifest.json`**, не `.xpi`

Так расширение пропадает после перезапуска Firefox.

**Firefox навсегда, без магазина** — unlisted-подпись Mozilla (файл свой, в каталоге AMO не появляется):

1. Аккаунт разработчика: [addons.mozilla.org/developers](https://addons.mozilla.org/developers/)
2. Ключи API: [addon/api/key](https://addons.mozilla.org/developers/addon/api/key/) — JWT issuer и JWT secret
3. В `.env` (не коммитить):

   ```
   WEB_EXT_API_KEY=user:12345678:123
   WEB_EXT_API_SECRET=секрет
   ```

4. Уникальный `version` в `extensions/firefox/manifest.json` (повтор той же версии AMO отклонит). Node.js подтянется сам в `.tools/node`, если `npx` нет в системе.
5. `make xpi-sign` — скачает подписанный файл в `dist/bookmarks.xpi` (id расширения: `bookmarks@local`, его не меняйте после первой подписи)
6. `about:addons` → шестерёнка → «Установить дополнение из файла» → `dist/bookmarks.xpi`

Первая подпись может занять несколько минут. Альтернатива без AMO: Firefox Developer Edition / Nightly и `xpinstall.signatures.required = false`.

В попапе URL сервера (для этой установки — `https://bookmarks.portfolio.com.ru`) и пароль. При первом запуске расширение создаёт `deviceId` в `storage.local`; если его стереть, сервер сбрасывает сессию.

- Хоткей (предлагается `Alt+Shift+D`) — сразу в последнюю папку
- Omnibox: в адресной строке `bm запрос`
- Импорт закладок браузера — новая корневая папка, без мержа
- В кабинете можно отключить требование входа для расширений (только для localhost)

## Данные

В git не попадают `.env`, `*.sqlite*`, `data/`, `backups/`. JSON-экспорт из настроек не содержит сессий.
