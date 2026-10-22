@echo off
chcp 65001 >nul
setlocal
rem start.bat - запуск компаньона одним двойным щелчком.
rem
rem Что делает по шагам:
rem   1) проверяет, что есть Go и Node - иначе понятное сообщение, а не мигнувшее окно;
rem   2) СОБИРАЕТ сервер каждый раз: серверный код без пересборки - это сервер и
rem      клиент из разных версий, то есть неуловимый баг "ничего не меняется";
rem   3) ставит оболочку Electron, если её ещё нет;
rem   4) открывает окно персонажа. Сервер окно поднимает само.
rem
rem ВАЖНО про текст ниже: круглые скобки, знаки "&" и "<" внутри строк echo
rem запрещены - cmd.exe разбирает их как свой синтаксис и падает с "was
rem unexpected at this time", не выполнив ни одной строки.
rem
rem Перезапуск после правки фронтенда: закрыть окно и запустить этот файл снова.
rem Проверка оболочки без человека: desktop\npx electron . --selftest
cd /d "%~dp0"

echo.
echo === Компаньон ===
echo.

where go >nul 2>nul
if errorlevel 1 (
  echo НЕ НАЙДЕН Go. Поставь Go 1.24 или новее: https://go.dev/dl/
  echo После установки запусти этот файл снова.
  pause
  exit /b 1
)

where node >nul 2>nul
if errorlevel 1 (
  echo НЕ НАЙДЕН Node.js. Поставь Node 18 или новее: https://nodejs.org/
  echo После установки запусти этот файл снова.
  pause
  exit /b 1
)

rem Прошлый запуск: его exe занят Windows, и сборка упала бы с "отказано в
rem доступе". Заодно это закрывает вторую беду - висеть на старом бинарнике,
rem то есть на старых исходниках, пока в файлах уже новый код.
echo [1/3] Останавливаю прошлый запуск, если он остался, и собираю сервер...
taskkill /IM companion.exe /F >nul 2>nul
taskkill /IM focuswatch.exe /F >nul 2>nul
taskkill /IM soundwatch.exe /F >nul 2>nul
rem Заодно закрываю прошлое ОКНО: оболочка тоже меняется (например, как она
rem держит окно на экране), а второе окно не открывается - компаньон один.
rem Без этой строки старое окно работало бы по старому коду.
taskkill /IM electron.exe /F >nul 2>nul
if not exist "bin" mkdir "bin"
pushd "server"
go build -o "..\bin\companion.exe" ./cmd/companion
if errorlevel 1 (
  popd
  echo.
  echo СБОРКА СЕРВЕРА НЕ УДАЛАСЬ - смотри сообщение об ошибке выше.
  pause
  exit /b 1
)
go build -o "..\bin\focuswatch.exe" ./cmd/focuswatch
if errorlevel 1 (
  popd
  echo.
  echo СБОРКА ПОМОЩНИКА ОКНА НЕ УДАЛАСЬ - смотри сообщение об ошибке выше.
  pause
  exit /b 1
)
go build -o "..\bin\soundwatch.exe" ./cmd/soundwatch
if errorlevel 1 (
  popd
  echo.
  echo СБОРКА ПОМОЩНИКА ЗВУКА НЕ УДАЛАСЬ - смотри сообщение об ошибке выше.
  pause
  exit /b 1
)
popd

if not exist "desktop\node_modules" (
  echo [2/3] Ставлю оболочку - один раз, скачает Electron, нужен интернет...
  pushd "desktop"
  call npm install
  if errorlevel 1 (
    popd
    echo.
    echo НЕ УДАЛОСЬ ПОСТАВИТЬ ОБОЛОЧКУ - npm install не сработал.
    echo Проверь интернет и запусти файл снова.
    pause
    exit /b 1
  )
  popd
) else (
  echo [2/3] Оболочка уже на месте.
)

echo [3/3] Запускаю персонажа...
echo Закрыть компаньона: закрыть это окно консоли. Второе окно не откроется - компаньон один.
pushd "desktop"
call npx electron .
set CODE=%ERRORLEVEL%
popd

if not "%CODE%"=="0" (
  echo.
  echo Компаньон закрылся с ошибкой %CODE%. Вывод выше.
  pause
)

endlocal
