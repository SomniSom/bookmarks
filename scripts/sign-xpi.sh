#!/usr/bin/env bash
# Unlisted-подпись Firefox XPI через AMO API (без витрины магазина).
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"

if [[ -f .env ]]; then
  set -a
  # shellcheck disable=SC1091
  . ./.env
  set +a
fi

if [[ -z "${WEB_EXT_API_KEY:-}" || -z "${WEB_EXT_API_SECRET:-}" ]]; then
  cat <<'EOF'
Нужны ключи AMO API (не публикуют расширение в магазине).

1. Войдите на https://addons.mozilla.org/developers/addon/api/key/
2. Создайте ключ (JWT issuer + JWT secret)
3. Положите в .env (файл в git не попадает):

   WEB_EXT_API_KEY=user:12345678:123
   WEB_EXT_API_SECRET=длинная-строка

4. Перед повторной подписью поднимите version в extensions/firefox/manifest.json
   и в extensions/chrome/manifest.json
5. make xpi-sign

Установка файла: about:addons → шестерёнка → «Установить дополнение из файла».
EOF
  exit 1
fi

# PATH, затем одноразовый Node LTS в .tools/node (не коммитится).
ensure_npx() {
  if command -v npx >/dev/null 2>&1; then
    return
  fi
  local node_dir="$root/.tools/node"
  if [[ -x "$node_dir/bin/npx" ]]; then
    export PATH="$node_dir/bin:$PATH"
    return
  fi
  local ver="v22.20.0"
  local arch
  case "$(uname -m)" in
    x86_64) arch=x64 ;;
    aarch64|arm64) arch=arm64 ;;
    *)
      echo "Нет готового Node для архитектуры $(uname -m). Поставьте Node.js и повторите make xpi-sign." >&2
      exit 1
      ;;
  esac
  local name="node-${ver}-linux-${arch}"
  local url="https://nodejs.org/dist/${ver}/${name}.tar.xz"
  local tmp
  tmp=$(mktemp -d)
  echo "Node.js не найден, скачиваю ${ver} в .tools/node (один раз)"
  mkdir -p "$root/.tools"
  if ! command -v curl >/dev/null 2>&1; then
    echo "Нужен curl, чтобы скачать Node.js." >&2
    exit 1
  fi
  curl -fsSL "$url" | tar -xJ -C "$tmp"
  rm -rf "$node_dir"
  mv "$tmp/$name" "$node_dir"
  rm -rf "$tmp"
  export PATH="$node_dir/bin:$PATH"
}

ensure_npx
if ! command -v npx >/dev/null 2>&1; then
  echo "Не удалось получить npx." >&2
  exit 1
fi

id=$(python3 -c 'import json; print(json.load(open("extensions/firefox/manifest.json"))["browser_specific_settings"]["gecko"]["id"])')
ver=$(python3 -c 'import json; print(json.load(open("extensions/firefox/manifest.json"))["version"])')

mkdir -p dist
rm -f dist/*.xpi

echo "Подпись unlisted: ${id} ${ver} (витрина AMO не создаётся)"

npx --yes web-ext@10 sign \
  --source-dir extensions/firefox \
  --artifacts-dir dist \
  --channel unlisted \
  --api-key "$WEB_EXT_API_KEY" \
  --api-secret "$WEB_EXT_API_SECRET"

newest=$(ls -1t dist/*.xpi 2>/dev/null | head -1 || true)
if [[ -z "$newest" ]]; then
  echo "web-ext не положил xpi в dist/" >&2
  exit 1
fi
if [[ "$(basename "$newest")" != "bookmarks.xpi" ]]; then
  cp -f "$newest" dist/bookmarks.xpi
fi

echo
echo "готово: $newest"
echo "копия: dist/bookmarks.xpi"
echo "Firefox → about:addons → шестерёнка → Установить дополнение из файла"
