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
# node_min — первая версия с фиксом CVE-2026-111497 (out-of-bounds запись в декодере
# Brotli); node_pin скачивается, если системный Node старше, и проверяется по SHA256SUMS.
node_min="v22.23.4"
node_pin="v24.22.1"

version_ge() {
  [[ -n "$1" ]] || return 1
  [[ "$(printf '%s\n%s\n' "$2" "$1" | sort -V | head -1)" == "$2" ]]
}

ensure_npx() {
  local sys_ver
  if command -v npx >/dev/null 2>&1 && command -v node >/dev/null 2>&1; then
    sys_ver=$(node --version 2>/dev/null || true)
    if version_ge "$sys_ver" "$node_min"; then
      return
    fi
    echo "Системный Node ${sys_ver:-неизвестен} старше ${node_min}, беру ${node_pin} из .tools/node" >&2
  fi
  local node_dir="$root/.tools/node"
  if [[ -x "$node_dir/bin/npx" ]] && version_ge "$("$node_dir/bin/node" --version 2>/dev/null || true)" "$node_min"; then
    export PATH="$node_dir/bin:$PATH"
    return
  fi
  local arch
  case "$(uname -m)" in
    x86_64) arch=x64 ;;
    aarch64|arm64) arch=arm64 ;;
    *)
      echo "Нет готового Node для архитектуры $(uname -m). Поставьте Node.js и повторите make xpi-sign." >&2
      exit 1
      ;;
  esac
  local name="node-${node_pin}-linux-${arch}"
  local url="https://nodejs.org/dist/${node_pin}/${name}.tar.xz"
  local tmp
  tmp=$(mktemp -d)
  echo "Скачиваю Node.js ${node_pin} в .tools/node (один раз)"
  mkdir -p "$root/.tools"
  for tool in curl tar sha256sum; do
    if ! command -v "$tool" >/dev/null 2>&1; then
      echo "Нужен $tool, чтобы скачать и проверить Node.js." >&2
      exit 1
    fi
  done
  curl -fsSL "$url" -o "$tmp/${name}.tar.xz"
  curl -fsSL "https://nodejs.org/dist/${node_pin}/SHASUMS256.txt" -o "$tmp/SHASUMS256.txt"
  local expected
  expected=$(awk -v f="${name}.tar.xz" '$2 == f { print $1 }' "$tmp/SHASUMS256.txt")
  if [[ -z "$expected" ]]; then
    echo "В SHA256SUMS нет ${name}.tar.xz — архив не проверен, скачивание прервано." >&2
    exit 1
  fi
  echo "${expected}  $tmp/${name}.tar.xz" | sha256sum -c - >/dev/null
  tar -xJf "$tmp/${name}.tar.xz" -C "$tmp"
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
