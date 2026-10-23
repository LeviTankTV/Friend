#!/bin/sh
# start.sh — запуск компаньона одним двойным щелчком на Linux (то же, что
# start.bat на Windows).
#
# Что делает по шагам:
#   1) проверяет, что есть Go и Node — иначе понятное сообщение;
#   2) СОБИРАЕТ сервер каждый раз: серверный код без пересборки — это сервер
#      и клиент из разных версий, то есть неуловимый баг «ничего не меняется»;
#   3) ставит оболочку Electron, если её ещё нет;
#   4) открывает окно персонажа. Сервер окно поднимает само.
#
# Понимание активного окна работает в X11-сессии (Xorg); в Wayland-сессии
# глобального слежения за окнами нет — персонаж живёт без «понимания экрана».
# Танец под музыку требует PulseAudio (у PipeWire — через pipewire-pulse).
#
# Перезапуск после правки фронтенда: закрыть окно и запустить этот файл снова.
# Проверка оболочки без человека: desktop && npx electron . --selftest
set -e
cd "$(dirname "$0")"

echo
echo "=== Компаньон ==="
echo

if ! command -v go >/dev/null 2>&1; then
  echo "НЕ НАЙДЕН Go. Поставь Go 1.24 или новее: https://go.dev/dl/"
  echo "После установки запусти этот файл снова."
  exit 1
fi

if ! command -v node >/dev/null 2>&1; then
  echo "НЕ НАЙДЕН Node.js. Поставь Node 18 или новее: https://nodejs.org/"
  echo "После установки запусти этот файл снова."
  exit 1
fi

# Прошлый запуск: его процессы не должны спорить с новым за позицию окна и
# сидеть на старых исходниках, пока в файлах уже новый код.
echo "[1/3] Останавливаю прошлый запуск, если он остался, и собираю сервер..."
pkill -x companion 2>/dev/null || true
pkill -x focuswatch 2>/dev/null || true
pkill -x soundwatch 2>/dev/null || true
pkill -f 'electron .*desktop' 2>/dev/null || true
mkdir -p bin
cd server
go build -o ../bin/companion ./cmd/companion
go build -o ../bin/focuswatch ./cmd/focuswatch
go build -o ../bin/soundwatch ./cmd/soundwatch
cd ..

if [ ! -d desktop/node_modules ]; then
  echo "[2/3] Ставлю оболочку — один раз, скачает Electron, нужен интернет..."
  (cd desktop && npm install)
else
  echo "[2/3] Оболочка уже на месте."
fi

echo "[3/3] Запускаю персонажа..."
echo "Закрыть компаньона: закрыть это окно терминала. Второе окно не откроется — компаньон один."
cd desktop
npx electron .
