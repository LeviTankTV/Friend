// main.js — оболочка компаньона на Electron.
//
// Задача оболочки ровно одна: дать персонажу окно, которого нет у браузера —
// прозрачное, без рамки, поверх всех, и уметь его двигать. Вся логика
// (персонаж, анимации, память, LLM) живёт во фронтенде и в Go-сервере, поэтому
// переезд на Tauri — это замена ЭТОГО файла, а не переписывание проекта.
//
// Окно грузит страницу с локального сервера: так фронтенд и канал /ws живут на
// одном origin, и не нужны ни file://, ни CORS.
//
// Сервер оболочка поднимает сама (если он ещё не запущен): пользователь должен
// запускать ОДИН файл, а не помнить про два процесса.

const { app, BrowserWindow, ipcMain, screen, shell, powerMonitor, session, Tray, Menu, nativeImage } = require('electron');
const { spawn } = require('node:child_process');
const path = require('node:path');
const fs = require('node:fs');
const http = require('node:http');

const DEV = process.argv.includes('--dev');
// Самопроверка оболочки: прогон перетаскивания без человека (см. runSelfTest).
const SELFTEST = process.argv.includes('--selftest');
const SERVER_ADDR = process.env.FRIEND_ADDR || '127.0.0.1:8787';
const APP_URL = `http://${SERVER_ADDR}/`;

let win = null;
let serverProc = null;
// Трей и «настоящий выход»: закрытие окна (крест, Alt+F4) прячет Миру в трей,
// и чтобы отличить «спряталась» от «пошла на выход», у выхода есть свой флаг —
// по нему окно отпускает себя само (см. createWindow, win.on('close')).
let tray = null;
// У Tray НЕТ getImage — только setImage, поэтому свою текущую иконку храним
// у себя: по ней самопроверка видит, что иконка реально непустая.
let trayImage = null;
let isQuitting = false;

// --- Пульс кадров ---------------------------------------------------------
//
// Страница каждый нарисованный кадр говорит «я жива» (web/js/main.js,
// bridge.beat). Молчание пульса — самый честный признак того, что персонажа на
// экране больше нет: список подключений, состояние окна и логи сервера при этом
// выглядят совершенно здоровыми.
const BEAT_TIMEOUT = 9000;      // сколько молчания = поломка отрисовки
const HEAL_COOLDOWN = 30000;    // как часто можно поднимать страницу
let lastBeat = 0;               // когда страница рисовала кадр
let lastHeal = 0;               // когда мы в последний раз её поднимали

ipcMain.on('page:beat', () => { lastBeat = Date.now(); });

// --- Сервер ---------------------------------------------------------------

function ping() {
  return new Promise((resolve) => {
    const req = http.get({ host: SERVER_ADDR.split(':')[0], port: Number(SERVER_ADDR.split(':')[1]), path: '/healthz', timeout: 700 }, (res) => {
      res.resume();
      resolve(res.statusCode === 200);
    });
    req.on('error', () => resolve(false));
    req.on('timeout', () => { req.destroy(); resolve(false); });
  });
}

async function ensureServer() {
  if (await ping()) {
    console.log('[friend] сервер уже запущен:', SERVER_ADDR);
    return true;
  }
  const exe = process.platform === 'win32' ? 'companion.exe' : 'companion';
  const candidates = [
    path.join(__dirname, '..', 'bin', exe),
    path.join(__dirname, '..', 'server', exe),
  ];
  const bin = candidates.find((p) => fs.existsSync(p));
  if (!bin) {
    console.error('[friend] не нашёл сервер. Собери его: cd server && go build -o ../bin/' + exe + ' ./cmd/companion');
    return false;
  }
  serverProc = spawn(bin, ['-web', path.join(__dirname, '..', 'web'), '-addr', SERVER_ADDR], {
    cwd: path.join(__dirname, '..'),
    stdio: ['ignore', 'pipe', 'pipe'],
    windowsHide: true,
  });
  serverProc.stdout.on('data', (d) => process.stdout.write('[server] ' + d));
  serverProc.stderr.on('data', (d) => process.stderr.write('[server] ' + d));
  serverProc.on('exit', (code) => { console.log('[friend] сервер завершился, код', code); serverProc = null; });

  for (let i = 0; i < 40; i++) {
    if (await ping()) {
      console.log('[friend] сервер поднят:', SERVER_ADDR);
      return true;
    }
    await new Promise((r) => setTimeout(r, 250));
  }
  console.error('[friend] сервер не ответил за 10 секунд');
  return false;
}

// --- Активное окно (focuswatch) -------------------------------------------
//
// Оболочка запускает маленького помощника: он спрашивает Windows, какое окно
// в фокусе (имя процесса + заголовок), и печатает строку, когда фокус сменился.
// Оболочка читает эти строки и передаёт странице, а страница — серверу
// (сообщение focus). Сервер кладёт «он сейчас в …» в системный промпт.
//
// Почему помощник отдельным процессом, а не цикл в оболочке: запросы к системному
// API должны стоить копейки и не мешать ни кадрам персонажа, ни серверу.
const FOCUS_INTERVAL_SEC = 4;
const FOCUS_RESTART_MS = 5000;

let focusProc = null;
let focusLines = 0;        // сколько смен окна помощник выдал (видно самопроверке)
let focusStopping = false; // гасим сами при выходе — не перезапускаем
let focusRestartTimer = null;
let lastFocusData = null;  // последнее окно от помощника — отдаём странице после её перезагрузки
// selftestTarget — окно-мишень для самопроверки посадки «на окне»: помощник
// в режиме -selftest выдаёт строку «SELFTEST-…», оболочка подставляет в неё
// эти границы (экранная геометрия своей мишени). Живая Миры их не видит:
// сервер строку-маркер отбрасывает.
let selftestTarget = null;

function ensureFocusWatch(selftest) {
  // Помощник есть только на Windows (на других системах активное окно — другие
  // API); персонаж при этом работает как раньше, просто без «понимания экрана».
  if (process.platform !== 'win32') return;
  const bin = [
    path.join(__dirname, '..', 'bin', 'focuswatch.exe'),
    path.join(__dirname, '..', 'server', 'focuswatch.exe'),
  ].find((p) => fs.existsSync(p));
  if (!bin) {
    console.log('[friend] focuswatch нет — компаньон работает без понимания окна ' +
      '(сборка: cd server && go build -o ..\\bin\\focuswatch.exe ./cmd/focuswatch)');
    return;
  }
  focusStopping = false;
  focusProc = spawn(bin, selftest
    ? ['-selftest', '-interval', '1s']
    : ['-interval', FOCUS_INTERVAL_SEC + 's'], {
    cwd: path.join(__dirname, '..'),
    stdio: ['pipe', 'pipe', 'pipe'], // stdin — «жив ли родитель»: закрылся Electron — вышел помощник
    windowsHide: true,
  });
  let buf = '';
  focusProc.stdout.on('data', (chunk) => {
    // Помощник печатает по JSON-строке на смену окна; режем поток на строки.
    buf += chunk;
    let i;
    while ((i = buf.indexOf('\n')) >= 0) {
      const line = buf.slice(0, i).trim();
      buf = buf.slice(i + 1);
      if (!line) continue;
      let data = null;
      try { data = JSON.parse(line); } catch (_err) { continue; }
      if (data && data.process) {
        // Тестовое окно посадки: границы мишени подставляет оболочка —
        // страница сажает персонажа ровно туда, куда знает самопроверка.
        if (selftestTarget && data.title && data.title.indexOf('SELFTEST-') === 0) {
          data.bounds = [selftestTarget.x, selftestTarget.y, selftestTarget.w, selftestTarget.h];
        }
        focusLines++;
        lastFocusData = data; // страница пришлёт его заново после переподключения
        if (win && !win.isDestroyed()) win.webContents.send('focus:info', data);
      }
    }
  });
  focusProc.stderr.on('data', (d) => process.stderr.write('[focuswatch] ' + d));
  focusProc.on('exit', () => {
    focusProc = null;
    if (focusStopping) return;
    // Помощник умер сам (краш, система сняла процесс) — перезапуск с паузой:
    // понимание окна не должно пропадать из-за одной смерти процесса.
    clearTimeout(focusRestartTimer);
    focusRestartTimer = setTimeout(ensureFocusWatch, FOCUS_RESTART_MS);
  });
}

function stopFocusWatch() {
  focusStopping = true;
  clearTimeout(focusRestartTimer);
  if (focusProc && !focusProc.killed) focusProc.kill();
  focusProc = null;
}

// --- Уровень звука на колонках (soundwatch) --------------------------------
//
// Для «танцует под музыку» (этап «реакция на звук»): помощник soundwatch.exe
// читает системный микшер (WASAPI loopback) и печатает в stdout ровно
// {"level":RMS}. Оболочка пересылает строку странице (js/music.js).
// Граница приватности: из звукового сигнала наружу уходит ОДНО число —
// содержимое звука нигде не сохраняется и никуда, кроме локальной страницы,
// не отправляется. Помощник живёт только пока включён «танец» (меню).
const SOUND_INTERVAL_MS = 200;
const SOUND_RESTART_MS = 5000;

let soundProc = null;
let soundWanted = false;   // человек включил танец — должен работать
let soundStopping = false;  // гасим сами (меню/выход) — не перезапускаем
let soundRestartTimer = null;
let soundLines = 0;         // сколько уровней дошло (видно самопроверке)

function ensureSoundWatch() {
  if (process.platform !== 'win32') return null;
  const bin = [
    path.join(__dirname, '..', 'bin', 'soundwatch.exe'),
    path.join(__dirname, '..', 'server', 'soundwatch.exe'),
  ].find((p) => fs.existsSync(p));
  if (!bin) {
    console.log('[friend] soundwatch нет — танец под музыку недоступен ' +
      '(сборка: cd server && go build -o ..\\bin\\soundwatch.exe ./cmd/soundwatch)');
    return null;
  }
  soundStopping = false;
  soundProc = spawn(bin, ['-interval', SOUND_INTERVAL_MS + 'ms'], {
    cwd: path.join(__dirname, '..'),
    stdio: ['pipe', 'pipe', 'pipe'], // stdin — «жив ли родитель»: закрылся Electron — вышел помощник
    windowsHide: true,
  });
  let buf = '';
  soundProc.stdout.on('data', (chunk) => {
    buf += chunk;
    let i;
    while ((i = buf.indexOf('\n')) >= 0) {
      const line = buf.slice(0, i).trim();
      buf = buf.slice(i + 1);
      if (!line) continue;
      let data = null;
      try { data = JSON.parse(line); } catch (_err) { continue; }
      if (data && typeof data.level === 'number') {
        soundLines++;
        if (win && !win.isDestroyed()) win.webContents.send('sound:level', data.level);
      }
    }
  });
  soundProc.stderr.on('data', (d) => process.stderr.write('[soundwatch] ' + d));
  soundProc.on('exit', () => {
    soundProc = null;
    if (soundStopping || !soundWanted) return;
    // Умер сам (сменили устройство, сняла система) — перезапуск с паузой:
    // танец не должен пропадать из-за одной смерти процесса.
    clearTimeout(soundRestartTimer);
    soundRestartTimer = setTimeout(ensureSoundWatch, SOUND_RESTART_MS);
  });
  return soundProc;
}

// Вкл/выкл по движению тумблера «танцевать под музыку».
function setSoundWatch(on) {
  soundWanted = !!on;
  clearTimeout(soundRestartTimer);
  if (soundWanted) {
    if (!soundProc) ensureSoundWatch();
  } else if (soundProc && !soundProc.killed) {
    soundStopping = true;
    soundProc.kill();
    soundProc = null;
  }
}

function stopSoundWatch() {
  soundWanted = false;
  soundStopping = true;
  clearTimeout(soundRestartTimer);
  if (soundProc && !soundProc.killed) soundProc.kill();
  soundProc = null;
}

// --- Настройки персонажа ---------------------------------------------------
//
// Размер и посадка — не состояние сервера, но и не «внутреннее дело страницы»:
// start.bat гасит прошлое окно СИЛОЙ (taskkill /F), и браузерное хранилище
// (localStorage) не успевает сброситься на диск. Настройки обнулялись при каждом
// запуске, и человек видел «посадка сбросилась, выхода из-за края нет» — хотя
// ничего не выбирал заново. Поэтому они лежат файлом рядом с состоянием, а
// оболочка отдаёт их странице при создании окна и записывает, когда они меняются.
// follow — сила слежения за курсором, 0..100 (ползунок в меню); раньше был
// булевым, старую запись понимает меню (см. pick в js/menu.js).
const DEFAULT_SETTINGS = { pix: 5, seat: 'free', keepTop: true, follow: 80, voice: true, wake: false, autostart: false };

function settingsPath() {
  return path.join(app.getPath('userData'), 'settings.json');
}

// readSettingsRaw — что лежит в файле НА САМОМ ДЕЛЕ: null, если файла нет или он
// испорчен. Нужна отдельно от readSettings: у той неотличим «первый запуск» и
// «настройки не сохраняются», а проверка обязана видеть разницу.
function readSettingsRaw() {
  try {
    const parsed = JSON.parse(fs.readFileSync(settingsPath(), 'utf8'));
    return parsed && typeof parsed === 'object' ? parsed : null;
  } catch (_err) {
    return null;
  }
}

function readSettings() {
  // Файла ещё нет (первый запуск) или он испорчен — значения по умолчанию.
  return Object.assign({}, DEFAULT_SETTINGS, readSettingsRaw() || {});
}

function writeSettings(json) {
  try {
    const file = settingsPath();
    const tmp = file + '.tmp';
    // Пишем через временный файл и переименование: обрыв записи не должен
    // оставить половину настроек (их читают при следующем запуске).
    fs.writeFileSync(tmp, JSON.stringify(json));
    fs.renameSync(tmp, file);
  } catch (err) {
    console.error('[friend] настройки не сохранились:', err.message);
  }
}

// --- Трей и автозапуск ----------------------------------------------------

// Зачем трей: у окна компаньона нет рамки и нет кнопки «крестик», человек убивает
// его через диспетчер задач или закрывает Alt+F4 — и персонаж исчезает совсем,
// вместе с сервером. С треем «закрыть» означает «спрятаться», а не «умереть»:
// Миру видно в уголке уведомлений, двойной клик по иконке возвращает её на экран.
// Плюс отсюда же — «выйти по-настоящему», с остановкой сервера.

// trayFaceImage — базовая иконка, которую рисуем сами (без файлов на диске):
// розовое личико в тёмной оправе — узнаётся на 16 пикселях. Она стоит СРАЗУ при
// старте, а когда страница собрана — иконку заменяем настоящим снимком окна
// (trayRefreshIcon), чтобы в трее была именно её текущая причёска и поза.
function trayFaceImage() {
  const size = 16;
  const buf = Buffer.alloc(size * size * 4);   // BGRA, как просит nativeImage на Windows
  for (let y = 0; y < size; y++) {
    for (let x = 0; x < size; x++) {
      const dx = x - (size - 1) / 2;
      const dy = y - (size - 1) / 2;
      const d = Math.sqrt(dx * dx + dy * dy);
      const i = (y * size + x) * 4;
      if (d > 7.6) continue;                   // за кругом — прозрачно
      const hair = d >= 6.3;                   // тёмная кайма «волос»
      buf[i] = hair ? 0x40 : 0xc8;             // B
      buf[i + 1] = hair ? 0x2c : 0xd8;         // G
      buf[i + 2] = hair ? 0x4a : 0xff;         // R
      buf[i + 3] = 255;                        // A
    }
  }
  return nativeImage.createFromBuffer(buf, { width: size, height: size });
}

function trayToggle() {
  if (!win) return;
  if (win.isVisible()) {
    win.hide();
  } else {
    win.show();
    win.focus();
  }
}

function createTray() {
  if (tray) return;
  try {
    trayImage = trayFaceImage();
    tray = new Tray(trayImage);
  } catch (err) {
    // На некоторых системах трей не поднимается (оболочка уведомлений не ответила).
    // Персонаж от этого не должен исчезать — просто остаёмся без трея.
    console.error('[friend] трей не поднялся: ' + err.message);
    tray = null;
    return;
  }
  tray.setToolTip('Мира — компаньон: двойной клик — показать, меню — выход');
  tray.setContextMenu(Menu.buildFromTemplate([
    { label: 'показать / скрыть', click: () => trayToggle() },
    { type: 'separator' },
    { label: 'выход', click: () => { app.quit(); } },
  ]));
  // Двойной клик по иконке — самое ожидаемое действие в трее Windows.
  tray.on('double-click', () => trayToggle());
  console.log('[friend] трей: поднят');
}

// trayRefreshIcon — снимок настоящего окна в иконку трея: capturePage берёт
// экранное содержимое вместе с прозрачностью, и в трее оказывается текущий вид
// персонажа. Окно при этом должно быть видно — после hide() снимок пустой,
// поэтому базовая иконка никогда не выкидывается, а только заменяется.
function trayRefreshIcon() {
  if (!tray || !win || !win.isVisible()) return;
  win.webContents.capturePage().then((img) => {
    const sz = img.getSize();
    if (!sz.width || !sz.height) return;
    trayImage = img.resize({ height: 16 });
    tray.setImage(trayImage);
  }).catch((err) => {
    console.error('[friend] снимок для трея не вышел: ' + err.message);
  });
}

// Автозапуск: штатный механизм Windows — ключ реест HKCU\...\CurrentVersion\Run,
// его Electron умеет писать сам (setLoginItemSettings). Команда запуска:
// тот же electron.exe с папкой приложения — то есть ровно тот же старт, что
// делает start.bat, только без окна консоли. Сработает и при запуске из
// самопроверки: там Electron тот же, папка та же.
function autostartApply(on) {
  try {
    app.setLoginItemSettings({
      openAtLogin: !!on,
      args: [__dirname],
      path: process.execPath,
    });
    return true;
  } catch (err) {
    console.error('[friend] автозапуск не применился: ' + err.message);
    return false;
  }
}

// На Windows openAtLogin читается СРАВНЕНИЕМ: запись в ключе автозапуска
// должна совпасть с той командой, которую спрашивают — путь и те же args,
// что писали. Прочитать без args — увидеть false при включённом запуске.
function autostartRead() {
  try {
    return app.getLoginItemSettings({ args: [__dirname] }).openAtLogin === true;
  } catch (_err) {
    return false;
  }
}

// --- Окно -----------------------------------------------------------------

function createWindow() {
  const primary = screen.getPrimaryDisplay();
  const wa = primary.workAreaSize;
  // Стартовый размер — как у персонажа на этапе 1; точный размер приедет из
  // welcome (bridge.setSize → ipc 'win:set-size').
  const width = 320;
  const height = 420;

  // Персонажа ещё нет — считаем, что кадров не было только что. Иначе страница,
  // которая не смогла собраться вообще (ошибка в скрипте), молчала бы вечно, и
  // сторож по пульсу её бы не тронул.
  lastBeat = Date.now();

  win = new BrowserWindow({
    width,
    height,
    x: Math.max(0, wa.width - width - 40),
    y: Math.max(0, wa.height - height - 80),
    transparent: true,        // окна нет — есть только персонаж
    frame: false,             // ни рамки, ни заголовка
    resizable: false,         // размер задаёт конфиг, а не пользователь
    hasShadow: false,
    skipTaskbar: true,        // компаньон не должен висеть в панели задач
    fullscreenable: false,
    maximizable: false,
    minimizable: true,
    alwaysOnTop: true,        // поверх всех окон — главное требование
    webPreferences: {
      preload: path.join(__dirname, 'preload.js'),
      contextIsolation: true, // оболочка не отдаёт фронтенду доступ к Node
      nodeIntegration: false,
      backgroundThrottling: false, // персонаж не «засыпает», когда окно не в фокусе
      // Настройки отдаём странице ДО её скриптов: выбор размера и посадки должен
      // работать с первого кадра, а не «приехать» через секунду после загрузки.
      additionalArguments: ['--friend-settings=' + JSON.stringify(readSettings())],
    },
  });

  win.setAlwaysOnTop(true, 'screen-saver');   // перекрывает и панель задач
  win.setVisibleOnAllWorkspaces(true, { visibleOnFullScreen: true });
  // Микрофон (этап 7, голосовой ввод): окно прозрачное и без фокуса — без
  // явного разрешения Chromium сам запрос на getUserMedia не пропускает.
  // Разрешаем ровно два: media (микрофон) и display-capture (запас на
  // случай, если страница попросит дорожку микшера; сам танец теперь идёт
  // через soundwatch.exe, без захвата экрана). Всё остальное (камера,
  // геометрия, уведомления) — отказ: у компаньона нет на эти права сценария.
  session.defaultSession.setPermissionRequestHandler((wc, permission, callback) => {
    callback(permission === 'media' || permission === 'display-capture');
  });
  if (DEV) {
    win.webContents.openDevTools({ mode: 'detach' });
  }
  // Прозрачное окно: клики по нему не должны уходить в «пустоту» за пределы
  // персонажа — этим занимается фронтенд (мышь ловит только фигура).

  win.loadURL(APP_URL).catch((err) => {
    console.error('[friend] не удалось загрузить', APP_URL, err.message);
    // Понятная подсказка вместо белого окна.
    win.loadURL('data:text/html;charset=utf-8,' + encodeURIComponent(
      '<body style="font:14px sans-serif;color:#eee;background:#111;padding:20px">' +
      'Сервер компаньона не отвечает на ' + APP_URL +
      '<br><br>Соберите и запустите его: <code>cd server && go run ./cmd/companion</code></body>'));
  });

  // Внешние ссылки — в браузер, а не внутрь окна персонажа.
  win.webContents.setWindowOpenHandler(({ url }) => { shell.openExternal(url); return { action: 'deny' }; });

  // Самопроверка оболочки в лог: если окно перестало быть прозрачным или
  // «поверх всех», это видно в выводе, а не только глазами.
  win.once('ready-to-show', () => {
    const b = win.getBounds();
    console.log('[friend] окно: bounds=' + b.x + ',' + b.y + ' ' + b.width + 'x' + b.height +
      ' alwaysOnTop=' + win.isAlwaysOnTop() + ' visible=' + win.isVisible());
    // Иконка трея = её текущий вид: ждём, пока страница получит welcome (размер,
    // поза, позиция) — снимок раньше покажет пустое окно.
    setTimeout(trayRefreshIcon, 2500);
  });

  // Закрытие окна = прятанье в трей, а не выход: крестика у рамки нет, но
  // Alt+F4 и «свернуть в трей» сторонними программами есть, и терять из-за
  // этого персонажа с сервером — худший сценарий. Окно гаснет, Миру видно в
  // уголке уведомлений, двойной клик по иконке возвращает её.
  // Настоящий выход (меню трея, «выход» в меню персонажа) ставит isQuitting —
  // тогда окно отпускает себя само, и before-quit гасит сервер как обычно.
  win.on('close', (e) => {
    if (!isQuitting) {
      e.preventDefault();
      win.hide();
    }
  });

  // Если страница упала или не загрузилась, окно остаётся ПУСТЫМ и прозрачным: со
  // стороны это выглядит как «персонаж исчез», хотя процесс живёт. Поэтому
  // поднимаем её обратно вместо того, чтобы ждать, пока человек догадается.
  win.webContents.on('render-process-gone', (_event, details) => {
    console.error('[friend] страница упала (' + (details && details.reason) + ') — перезагружаю');
    win.reload();
  });
  win.webContents.on('unresponsive', () => {
    console.error('[friend] окно не отвечает — перезагружаю страницу');
    win.reload();
  });
  win.webContents.on('did-finish-load', () => {
    lastBeat = Date.now();
    // Страница перезагрузилась (лечение, правка кода): отдаём ей последнее
    // окно помощника заново — сервер продолжит собирать обстановку, а не
    // начнёт с нуля.
    if (lastFocusData) win.webContents.send('focus:info', lastFocusData);
  });
  win.webContents.on('did-fail-load', (_event, code, desc, url) => {
    console.error('[friend] страница не загрузилась: ' + code + ' ' + desc + ' (' + url + ')');
    setTimeout(() => {
      if (win) win.loadURL(APP_URL).catch(() => {});
    }, 1500);
  });
  // Ошибки со страницы печатаем в консоль оболочки: без этого поломка в
  // js/avatar.js выглядит как «персонаж исчез» без единого слова о причине.
  win.webContents.on('console-message', (...args) => {
    const first = args[0];
    const level = first && typeof first === 'object' && 'level' in first ? first.level : args[1];
    const message = first && typeof first === 'object' && 'message' in first ? first.message : args[2];
    if (level === 3 || level === 'error') console.error('[friend] ошибка на странице:', message);
  });

  win.on('closed', () => { win = null; });
}

// --- Держим окно на экране ------------------------------------------------

// Видимая часть окна, которой достаточно, чтобы его можно было ухватить мышью.
const MIN_VISIBLE = 48;

// clampToScreen — привести позицию окна в видимую часть рабочего стола.
//
// Зачем это нужно: позиция окна — это его ПАМЯТЬ. Сохранённая точка живёт
// дольше, чем обстоятельства, в которых её записали: монитор отключают,
// разрешение меняют, масштаб ставят другой. Окно, поставленное по такой
// памяти, уезжает туда, откуда его не достать мышью, — оно просто не видно, и
// починить это можно только правкой файла состояния. Поэтому через проверку
// проходит КАЖДЫЙ переезд: и восстановление сохранённой позиции, и
// перетаскивание.
function clampToScreen(x, y, width, height) {
  const displays = screen.getAllDisplays();
  const primary = screen.getPrimaryDisplay();
  const w = Math.max(1, Math.round(width || 320));
  const h = Math.max(1, Math.round(height || 420));
  const fits = (area) => Math.min(x + w, area.x + area.width) - Math.max(x, area.x) >= MIN_VISIBLE &&
    Math.min(y + h, area.y + area.height) - Math.max(y, area.y) >= MIN_VISIBLE;

  // Монитор, которому принадлежит точка: тот, где окно хотя бы частично видно.
  const target = displays.find((d) => fits(d.workArea));
  if (target) {
    const a = target.workArea;
    // По краям разрешён СВЕС. Раньше окно загонялось внутрь целиком, и это
    // ломало две вещи сразу: «посадить её у края экрана, чтобы выглядывала»
    // становилось невозможно, а персонаж, которого мышью утянули к краю, упирался
    // в невидимую стенку и оставлял на экране пустое окно. Неприкосновенным
    // остаётся только MIN_VISIBLE: сколько-то пикселей всегда видно, ухватить
    // мышью есть за что.
    return {
      x: Math.round(Math.min(Math.max(x, a.x - (w - MIN_VISIBLE)), a.x + a.width - MIN_VISIBLE)),
      y: Math.round(Math.min(Math.max(y, a.y - (h - MIN_VISIBLE)), a.y + a.height - MIN_VISIBLE)),
    };
  }

  // Точка не попадает ни на один экран — возвращаемся в домашний угол
  // основного монитора: пустое место на рабочем столе лучше пропавшего окна.
  const a = primary.workArea;
  return {
    x: Math.round(Math.max(a.x, a.x + a.width - w - 40)),
    y: Math.round(Math.max(a.y, a.y + a.height - h - 80)),
  };
}

// --- Размер окна ----------------------------------------------------------
//
// wantSize — размер, который попросила страница (персонаж плюс его поля, плюс
// полоса меню, когда оно открыто). Оболочка ПОМНИТ его и всегда применяет
// ВМЕСТЕ с позицией: окно компаньона не должно менять размер само — вместе с ним
// растянутся облачко реплик и поле ввода, то есть ровно то, на что жалуются
// живые люди («диалоговое окно и бар на весь экран»).
//
// Почему это отдельное правило, а не «подвинуть окно setPosition вместо
// setBounds». Замер на этой машине (Windows, масштаб экрана 125%):
// BrowserWindow.setPosition растит окно примерно на 1.3 пикселя ЗА ВЫЗОВ —
// 60 переездов дали 320×420 → 401×504, ещё 120 → 561×664, — при этом сама
// позиция не менялась вовсе. Протяжка мышью делает ~60 таких вызовов в секунду,
// так что окно раздувалось на глазах. setBounds с ЯВНЫМ размером этим не
// страдает: те же 200 переездов оставили размер на месте и довезли окно пиксель
// в пиксель. Отсюда правило: размер берём только у страницы, никогда не читаем
// его обратно из getBounds (такой «круговорот» тоже растит окно — проверено).
let wantSize = { width: 320, height: 420 };

// saneSize — разумные пределы размера. Страховка от опечатки и от чужого
// значения: окно не может стать больше любого экрана.
function saneSize(w, h) {
  const clamp = (v, fallback) => Math.max(160, Math.min(4096, Math.round(Number(v) || fallback)));
  return { width: clamp(w, wantSize.width), height: clamp(h, wantSize.height) };
}

// placeWindow — единственный путь переезда окна. Возвращает ФАКТИЧЕСКУЮ
// позицию: страница сравнивает её с запрошенной и сообщает серверу настоящее
// место, поэтому сохранённый адрес сам приходит в порядок.
function placeWindow(targetX, targetY, size) {
  if (!win) return { x: 0, y: 0 };
  const s = size || wantSize;
  const p = clampToScreen(targetX, targetY, s.width, s.height);
  win.setBounds({ x: p.x, y: p.y, width: s.width, height: s.height });
  const real = win.getBounds();
  // Расхождение в пиксель-два — это округление масштаба экрана (125% даёт +1).
  // Больше — уже поломка: возвращаем СВОЙ размер.
  if (Math.abs(real.width - s.width) > 2 || Math.abs(real.height - s.height) > 2) {
    console.error('[friend] окно само сменило размер: ' + real.width + 'x' + real.height +
      ' вместо ' + s.width + 'x' + s.height + ' — возвращаю');
    win.setBounds({ x: real.x, y: real.y, width: s.width, height: s.height });
  }
  const [nx, ny] = win.getPosition();
  return { x: nx, y: ny };
}

// ensureOnScreen — сторож окна.
//
// Зачем он нужен. Позиция окна — это память, которая живёт дольше обстоятельств:
// монитор отключают, разрешение и масштаб меняют, систему запускают с другой
// конфигурацией экранов. Окно, поставленное по такой памяти, оказывается за
// пределами рабочего стола — и персонаж «исчезает»: его не видно и мышью не
// достать. Поэтому периодически (и на любое изменение экранов) проверяем три
// вещи: окно не свёрнуто, оно видно и оно «поверх всех». Что не так — чиним.
function ensureOnScreen(why) {
  if (!win) return;
  if (win.isMinimized()) {
    win.restore();
    console.log('[friend] окно было свёрнуто — вернул (' + why + ')');
  }
  if (!win.isVisible()) {
    win.show();
    console.log('[friend] окно было скрыто — показал (' + why + ')');
  }
  const b = win.getBounds();
  const p = clampToScreen(b.x, b.y, b.width, b.height);
  if (p.x !== b.x || p.y !== b.y) {
    win.setPosition(p.x, p.y);
    console.log('[friend] окно вернулось на экран (' + why + '): ' +
      b.x + ',' + b.y + ' → ' + p.x + ',' + p.y);
    // Серверу тоже сообщаем настоящее место: иначе в памяти останется адрес за
    // экраном, и окно будет уезжать в никуда при каждом запуске.
    win.webContents.executeJavaScript(
      'window.friendApp && window.friendApp.bridge.getPosition().then(function (p) {' +
      ' window.friendApp.net.sendPosition(p.x, p.y); })',
    ).catch(() => {});
  }
  if (!win.isAlwaysOnTop()) {
    win.setAlwaysOnTop(true, 'screen-saver');
    console.log('[friend] «поверх всех» было потеряно — включил снова (' + why + ')');
  }
  // Размер окна тоже под присмотром. Окну компаньона незачем менять размер
  // между переездами, а вместе с раздувшимся окном растягиваются облачко реплик
  // и поле ввода. Поэтому если размер уехал от того, который попросила страница,
  // возвращаем его.
  if (Math.abs(b.width - wantSize.width) > 2 || Math.abs(b.height - wantSize.height) > 2) {
    // Пиксель-два — это округление масштаба экрана (125% даёт лишний пиксель),
    // такое возвращаем молча. А вот заметное расхождение — уже поломка, о ней
    // надо видеть в логе.
    const big = Math.abs(b.width - wantSize.width) > 8 || Math.abs(b.height - wantSize.height) > 8;
    const line = '[friend] окно само сменило размер: ' + b.width + 'x' + b.height +
      ' вместо ' + wantSize.width + 'x' + wantSize.height + ' — возвращаю (' + why + ')';
    if (big) console.error(line); else console.log(line);
    win.setBounds({ x: b.x, y: b.y, width: wantSize.width, height: wantSize.height });
  }
}

// checkDrawing — «а она вообще рисуется?».
//
// Сторож окна (ensureOnScreen) следит за ОКНОМ: не свёрнуто, видно, поверх всех,
// на экране. Этого мало. Есть поломка, при которой с окном всё хорошо, а
// персонажа на экране нет: цикл отрисовки внутри страницы остановился —
// подвисший драйвер, потерянный контекст WebGL, исключение в кадре. Снаружи это
// неотличимо от «она исчезла»: подключение живое, сервер отвечает, логи чистые.
//
// Признак — молчание пульса (страница бьёт им на каждом НАРИСОВАННОМ кадре,
// web/js/main.js). Но одного молчания мало: кадры перестают приходить и тогда,
// когда окно целиком перекрыто другой программой — система честно перестаёт его
// рисовать, а страница при этом здорова. Перезагрузка в этом случае — мигание и
// потеря набранного текста на ровном месте. Поэтому у страницы сначала
// спрашивают, жив ли её цикл: «остановлен» — поломка, лечим; «рисует» — кадры
// задерживает система, ждём; молчит — страница не отвечает вовсе (это обычно уже
// заметил обработчик unresponsive).
async function checkDrawing(why) {
  if (!win || !win.isVisible() || win.isMinimized() || win.webContents.isLoading()) return;
  const now = Date.now();
  if (lastBeat <= 0 || now - lastBeat <= BEAT_TIMEOUT) return;
  if (now - lastHeal <= HEAL_COOLDOWN) return;
  const silentSec = Math.round((now - lastBeat) / 1000);

  let state = 'молчит';
  try {
    state = await Promise.race([
      win.webContents.executeJavaScript(
        'window.friendApp && window.friendApp.loopState ? window.friendApp.loopState() : "нет страницы"'),
      new Promise((r) => setTimeout(() => r('молчит'), 3000)),
    ]);
  } catch (_err) {
    state = 'молчит';
  }
  if (!win) return;

  if (state === 'рисует') {
    // Кадров нет, а цикл жив: это система не отдаёт кадры (окно перекрыто,
    // экран погашен). Лечить нечего — но и молчать нельзя: в логе должно быть
    // видно, что персонажа нет на экране и почему.
    if (now - lastHeal > HEAL_COOLDOWN * 4) {
      lastHeal = now;
      console.log('[friend] кадров нет ' + silentSec + ' с, но страница жива (' + why + ') — не трогаю');
    }
    return;
  }

  lastHeal = now;
  lastBeat = now;
  console.error('[friend] картинка не обновляется ' + silentSec + ' с (' + state +
    ') — перезагружаю страницу (' + why + ')');
  win.reload();
}

// moveWindow ставит окно с проверкой на экран и возвращает ФАКТИЧЕСКУЮ позицию.
// Размер берётся у страницы (wantSize), а не у окна: см. «Размер окна» выше —
// именно чтение размера у окна и переезд через setPosition раздували его.
function moveWindow(targetX, targetY) {
  return placeWindow(targetX, targetY, wantSize);
}

// --- IPC от фронтенда -----------------------------------------------------

ipcMain.handle('win:get-position', () => {
  if (!win) return { x: 0, y: 0 };
  const [x, y] = win.getPosition();
  return { x, y };
});

ipcMain.handle('win:set-position', (_e, x, y) => {
  // Позиция — в логических пикселях: и setPosition, и getPosition работают в
  // них, поэтому масштаб дисплея (125%, 150%) учитывается один раз, а не дважды.
  return moveWindow(x, y);
});

// Переезд на дельту. Дельта считается от НАСТОЯЩЕЙ позиции, а сам переезд идёт
// через moveWindow → setBounds с явным размером.
//
// Почему не setPosition (как было раньше): замер показал, что на этой системе
// setPosition растит окно примерно на 1.3 пикселя за вызов и при этом может не
// сдвинуть его вовсе — за пару секунд протяжки окно раздувалось на весь экран.
// Прежняя причина выбирать setPosition ("setBounds подгонял рамку, окно
// проезжало 46 вместо 50") проверена заново: с явным размером 200 переездов
// подряд довозят окно пиксель в пиксель.
ipcMain.handle('win:move-by', (_e, dx, dy) => {
  if (!win) return { x: 0, y: 0 };
  const [x, y] = win.getPosition();
  // За край не уехать — значит и в памяти не останется точки за пределами экрана.
  return moveWindow(x + dx, y + dy);
});

ipcMain.handle('win:set-size', (_e, w, h) => {
  if (!win) return;
  wantSize = saneSize(w, h);
  const [x, y] = win.getPosition();
  placeWindow(x, y, wantSize);
});

// setBounds — переезд окна целиком (позиция + размер) одним вызовом. Нужен
// странице (bridge.setBounds) там, где два отдельных вызова дали бы видимый
// «прыжок»: сначала размер, потом позиция.
ipcMain.handle('win:set-bounds', (_e, b) => {
  if (!win) return { x: 0, y: 0, width: 0, height: 0 };
  const cur = win.getPosition();
  const x = Math.round(b && typeof b.x === 'number' ? b.x : cur[0]);
  const y = Math.round(b && typeof b.y === 'number' ? b.y : cur[1]);
  // Размер запоминаем как «то, что хочет страница» и применяем его же. Обратно
  // размер у окна НЕ спрашиваем: у окна без рамки размер содержимого и размер
  // окна совпадают, а чтение размера из getBounds и возврат его в setBounds сам
  // по себе растит окно (проверено замером — см. «Размер окна» выше).
  if (b && b.width && b.height) wantSize = saneSize(b.width, b.height);
  const at = placeWindow(x, y, wantSize);
  return { x: at.x, y: at.y, width: wantSize.width, height: wantSize.height };
});

// workArea — рабочая область экрана: у Windows она БЕЗ панели задач, поэтому её
// нижняя кромка и есть верх панели задач. Именно туда садится персонаж
// (js/main.js, applySeat), и именно поэтому это считает оболочка, а не страница:
// панель задач — часть системы, а не окна.
ipcMain.handle('screen:work-area', () => {
  const d = win && !win.isDestroyed() ? screen.getDisplayMatching(win.getBounds()) : screen.getPrimaryDisplay();
  const a = d.workArea;
  return { x: a.x, y: a.y, width: a.width, height: a.height };
});

ipcMain.handle('win:always-on-top', (_e, on) => {
  if (!win) return;
  win.setAlwaysOnTop(!!on, 'screen-saver');
});

ipcMain.handle('win:focus', () => {
  if (!win) return;
  // Показать и забрать фокус: без этого набранный в поле ввода текст уходит в
  // программу под окном — окно висит поверх всех, но фокуса у него нет.
  win.show();
  win.focus();
});

ipcMain.handle('app:quit', () => {
  app.quit();
});

// Автозапуск из меню персонажа: страница говорит «включи/выключи», оболочка
// пишет штатный ключ автозапуска Windows. Ответ — удалось ли (страница
// показывает правду, а не «нажал и забыл»).
ipcMain.handle('app:autostart', (_e, on) => autostartApply(!!on));

// «Танцует под музыку» (этап «реакция на звук»): тумблер из меню.
// On — оболочка поднимает soundwatch.exe, и его строки {"level":…} летят
// странице каналом 'sound:level'; off — помощник гасится.
// Приватность: наружу от звукового сигнала уходит РОВНО RMS-число, и только
// в локальную страницу; содержимое звука нигде не сохраняется.
ipcMain.handle('app:dance', (_e, on) => {
  if (!on) { setSoundWatch(false); return false; }
  setSoundWatch(true);
  return !!soundProc; // помощник поднялся? (нет бинаря/не Windows — false)
});

// Канал send, а не invoke: настройки сохраняются на каждое движение ползунка, и
// ждать на них ответ странице незачем.
ipcMain.on('app:save-settings', (_e, json) => {
  if (!json || typeof json !== 'object') return;
  writeSettings(json);
});

// --- Самопроверка оболочки ------------------------------------------------

// Зачем: перетаскивание — это цепочка «страница → preload → IPC → окно
// Windows». Разрыв в любом звене выглядит одинаково: «мышка не работает» — и
// искать причину глазами в четырёх файлах. Этот прогон дёргает НАСТОЯЩУЮ
// страницу синтетическими pointer-событиями и печатает, куда окно уехало на
// самом деле. Запуск: npx electron . --selftest (код возврата 0 = сдвинулось).
// serverPosition — сохранённая позиция окна из /healthz. По ней самопроверка
// видит, что сервер знает ПРАВИЛЬНЫЙ адрес персонажа, а не тот, что уводил его
// за пределы экрана.
function serverPosition() {
  return healthJson('position');
}

// serverFocus — активное окно человека, как его запомнил сервер (/healthz).
// По этой строке видно, дошла ли цепочка focuswatch → оболочка → страница →
// сервер целиком.
function serverFocus() {
  return healthJson('focus');
}

function healthJson(key) {
  return new Promise((resolve) => {
    const req = http.get({
      host: SERVER_ADDR.split(':')[0],
      port: Number(SERVER_ADDR.split(':')[1]),
      path: '/healthz',
      timeout: 1000,
    }, (res) => {
      let raw = '';
      res.on('data', (chunk) => { raw += chunk; });
      res.on('end', () => {
        try { resolve(JSON.parse(raw)[key] || null); } catch { resolve(null); }
      });
    });
    req.on('error', () => resolve(null));
    req.on('timeout', () => { req.destroy(); resolve(null); });
  });
}

// quietMouse — на время синтетических протяжек окно не видит НАСТОЯЩУЮ мышь.
//
// Зачем: проверка гоняет окно по экрану синтетическими событиями, а живой курсор
// над ползущим окном подмешивает свои pointermove — протяжка честно уходит за
// ним, и замер получается случайным. setIgnoreMouseEvents отсекает системную
// мышь, а события, созданные в самой странице, при этом доходят как обычно.
function quietMouse(on) {
  if (win && !win.isDestroyed()) win.setIgnoreMouseEvents(!!on);
}

async function runSelfTest() {
  quietMouse(true);
  // Запоминаем выбор человека ДО проверки — в конце вернём его на место.
  const keepSettings = readSettings();

  // Договор окна и страницы — проверяется ПЕРВЫМ делом, пока окно стоит там,
  // куда его поставила страница. Это самая дорогая поломка из всех, что тут
  // ловились: окно меньше персонажа — и от неё остаётся одна голова ("тела не
  // видно"), меню уезжает от неё, «выход» из-за края считает по чужой
  // геометрии, а сторож размера каждые три секунды возвращает окно к чужому
  // числу, и картинка дёргается. Причина была в приветствии: страница отдавала
  // оболочке размер из конфига сервера вместо своего, и при каждом
  // переподключении к серверу настоящий размер окна терялся.
  let agree = { ok: false, why: 'страница не ответила' };
  try {
    agree = await win.webContents.executeJavaScript(`(async () => {
      const app = window.friendApp;
      if (!app) return { error: 'страница не собралась: нет window.friendApp' };
      let saved = null;
      try { saved = JSON.parse(localStorage.getItem('friend.settings.v1')); } catch (_e) { saved = null; }
      const r = app.app.view.getBoundingClientRect();
      return {
        want: app.windowSize(),
        sprite: {
          w: Math.round(app.avatar.width), h: Math.round(app.avatar.height),
          pix: Math.round(app.avatar.scale() * 5), pose: app.avatar.pose(),
        },
        saved: saved,
        uiW: getComputedStyle(document.documentElement).getPropertyValue('--ui-w').trim(),
        canvas: { w: Math.round(r.width), h: Math.round(r.height) },
        // Габарит спрайта в координатах полотна. Нужен потому, что «окно
        // согласны с размером» ещё не значит «персонажа видно целиком»: пока
        // центр спрайта ставился на её ноги (js/main.js, layout), окно было
        // ровно того размера, который просила страница, проверка проходила — а
        // на экране была одна голова: низ фигуры уезжал под кромку окна.
        spriteBox: {
          left: Math.round(app.avatar.container.x - app.avatar.width / 2),
          right: Math.round(app.avatar.container.x + app.avatar.width / 2),
          top: Math.round(app.avatar.container.y - app.avatar.height / 2),
          bottom: Math.round(app.avatar.container.y + app.avatar.height / 2),
        },
        peek: { dir: app.peek.dir(), out: Number(app.peek.out().toFixed(2)) },
      };
    })()`);
    const real = win.getBounds();
    const near = (a, b) => Math.abs(a - b) <= 4;
    // Спрайт целиком в полотне: допускаем один пиксель округления, но не
    // половину фигуры. Иначе человек говорит «тела не видно», а проверки молчат.
    const box = agree.spriteBox;
    const spriteInside = !!box && !!agree.canvas &&
      box.left >= -1 && box.top >= -1 &&
      box.right <= agree.canvas.w + 1 && box.bottom <= agree.canvas.h + 1;
    agree.ok = !agree.error && near(real.width, agree.want.w) && near(real.height, agree.want.h) && spriteInside;
    agree.why = 'окно ' + real.width + 'x' + real.height +
      ', страница просит ' + (agree.want ? agree.want.w + 'x' + agree.want.h : '?') +
      ', спрайт ' + (agree.sprite ? agree.sprite.w + 'x' + agree.sprite.h + ' (клетка ' +
        agree.sprite.pix + ' px, поза ' + agree.sprite.pose + ')' : '?') +
      ', полотно ' + (agree.canvas ? agree.canvas.w + 'x' + agree.canvas.h : '?') +
      ', спрайт в полотне ' + (box ? box.left + ',' + box.top + '..' + box.right + ',' + box.bottom +
        (spriteInside ? ' → целиком виден' : ' → ОБРЕЗАН кромкой окна') : '?') +
      ', поле интерфейса ' + agree.uiW +
      ', настройки ' + JSON.stringify(agree.saved) +
      ', выход ' + (agree.peek ? agree.peek.dir + '/' + agree.peek.out : '?');
  } catch (err) {
    agree = { ok: false, why: err.message };
  }
  console.log('[friend] selftest: окно и страница — ' + agree.why + ' → ' +
    (agree.ok ? 'OK (окно и страница согласны)' : 'ОШИБКА (окно и страница расходятся)'));

  // Проверки двигают окно, поэтому дальше ставим его в заведомо безопасную
  // точку. У края экрана оболочка ограничивает переезд (и правильно делает), а
  // нам нужно померить ровно 100 на 50 пикселей.
  moveWindow(200, 300);
  const before = win.getBounds();
  let page = null;
  try {
    page = await win.webContents.executeJavaScript(`(async () => {
      const app = window.friendApp;
      if (!app) return { error: 'страница не собралась: нет window.friendApp' };
      const rect = app.app.view.getBoundingClientRect();
      const cx = rect.left + app.avatar.container.x;
      const cy = rect.top + app.avatar.container.y;
      const base = { bubbles: true, cancelable: true, pointerId: 1, isPrimary: true, buttons: 1, clientX: cx, clientY: cy };
      const ev = (type, extra) => Object.assign({}, base, extra);
      document.getElementById('stage').dispatchEvent(new PointerEvent('pointerdown', ev('pointerdown', { screenX: 1000, screenY: 500 })));
      // Шаги идут с паузой, как у настоящей мыши: только тогда между шагами
      // успевает пройти ответ оболочки, протяжка узнаёт точку отсчёта и переходит
      // на абсолютные цели. Пять шагов подряд без паузы — это не протяжка, а
      // пачка событий без ни одного ответа: там точка отсчёта вычисляется уже по
      // «съеденному» округлением результату, и ошибка остаётся в ней навсегда.
      for (let i = 1; i <= 5; i++) {
        window.dispatchEvent(new PointerEvent('pointermove', ev('pointermove', { screenX: 1000 + i * 20, screenY: 500 + i * 10 })));
        await new Promise((r) => setTimeout(r, 6));
      }
      window.dispatchEvent(new PointerEvent('pointerup', ev('pointerup', { screenX: 1100, screenY: 550 })));
      await new Promise((r) => setTimeout(r, 400));
      const pos = await app.bridge.getPosition();
      // На сервер отправляем ФАКТИЧЕСКОЕ место окна, а не то, что лежало в
      // памяти: самопроверка — это не переезд персонажа, но если сохранённый
      // адрес был за пределами экрана, его как раз и должно заменить правильное.
      app.net.sendPosition(pos.x, pos.y);
      await new Promise((r) => setTimeout(r, 300));
      // Точка отсчёта и цель протяжки — чтобы расхождение можно было разложить
      // на «попросили не туда» и «попросили туда, но окно не доехало».
      return { bridge: pos, via: app.bridge.mode, origin: app.drag.origin(), target: app.drag.target() };
    })()`);
  } catch (err) {
    console.error('[friend] selftest: страница не ответила:', err.message);
    app.exit(1);
    return;
  }

  const after = win.getBounds();
  const dx = after.x - before.x;
  const dy = after.y - before.y;
  const dragged = Math.abs(dx - 100) <= 2 && Math.abs(dy - 50) <= 2;
  if (page && page.error) console.error('[friend] selftest: ошибка страницы:', page.error);
  console.log('[friend] selftest: мост=' + (page ? page.via : '?') +
    ' окно ' + before.x + ',' + before.y + ' → ' + after.x + ',' + after.y +
    ' (сдвиг ' + dx + ',' + dy + ', ожидалось 100,50)' +
    ' цель протяжки ' + (page && page.target ? page.target.x + ',' + page.target.y : '?') +
    ' от ' + (page && page.origin ? page.origin.x + ',' + page.origin.y : '?') +
    ' → ' + (dragged ? 'OK' : 'ОШИБКА'));

  // Вторая половина проверки: окно компаньона — прямоугольник, но хватать
  // мышь должен только персонаж, иначе он отнимает клики у рабочего стола.
  const outside = win.getBounds();
  try {
    await win.webContents.executeJavaScript(`(async () => {
      const app = window.friendApp;
      const base = { bubbles: true, cancelable: true, pointerId: 2, isPrimary: true, buttons: 1, clientX: 3, clientY: 3 };
      document.getElementById('stage').dispatchEvent(new PointerEvent('pointerdown', Object.assign({}, base, { screenX: 2000, screenY: 300 })));
      for (let i = 1; i <= 5; i++) {
        window.dispatchEvent(new PointerEvent('pointermove', Object.assign({}, base, { screenX: 2000 + i * 20, screenY: 300 + i * 10 })));
      }
      window.dispatchEvent(new PointerEvent('pointerup', Object.assign({}, base, { screenX: 2100, screenY: 350 })));
      await new Promise((r) => setTimeout(r, 300));
    })()`);
  } catch (err) {
    console.error('[friend] selftest: страница не ответила на вторую часть:', err.message);
  }
  const held = win.getBounds();
  const gated = held.x === outside.x && held.y === outside.y;
  console.log('[friend] selftest: протяжка за пустое место — окно ' +
    outside.x + ',' + outside.y + ' → ' + held.x + ',' + held.y +
    ' → ' + (gated ? 'OK (не сдвинулось)' : 'ОШИБКА (персонаж должен ловить мышь только на себе)'));

  // Протяжка не должна менять РАЗМЕР окна — и это отдельная проверка, потому что
  // это была самая заметная поломка: окно росло на глазах, а вместе с ним
  // растягивались облачко реплик и поле ввода («диалоговое окно на весь экран»).
  // Причина оказалась в оболочке: BrowserWindow.setPosition на этой системе
  // (Windows, масштаб экрана 125%) растит окно примерно на 1.3 пикселя за вызов,
  // а протяжка делает их шестьдесят в секунду. Переезд теперь идёт через setBounds
  // с ЯВНЫМ размером, и размер обязан остаться прежним и после сотни шагов, и
  // окно — доехать (иначе «лечение» просто сломало бы перетаскивание).
  moveWindow(200, 300);
  await new Promise((r) => setTimeout(r, 200));
  const sizeBefore = win.getBounds();
  try {
    await win.webContents.executeJavaScript(`(async () => {
      const app = window.friendApp;
      const rect = app.app.view.getBoundingClientRect();
      const cx = rect.left + app.avatar.container.x;
      const cy = rect.top + app.avatar.container.y;
      const base = { bubbles: true, cancelable: true, pointerId: 9, isPrimary: true, buttons: 1, clientX: cx, clientY: cy };
      document.getElementById('stage').dispatchEvent(new PointerEvent('pointerdown', Object.assign({}, base, { screenX: 600, screenY: 400 })));
      // Шаги идут с паузой, как настоящая мышь: события пачкой без паузы — это не
      // проверка, а нагрузочный тест очереди IPC.
      for (let i = 1; i <= 120; i++) {
        window.dispatchEvent(new PointerEvent('pointermove', Object.assign({}, base, { screenX: 600 + i * 4, screenY: 400 + i * 2 })));
        await new Promise((r) => setTimeout(r, 4));
      }
      window.dispatchEvent(new PointerEvent('pointerup', Object.assign({}, base, { screenX: 1080, screenY: 640 })));
      await new Promise((r) => setTimeout(r, 800));
    })()`);
  } catch (err) {
    console.error('[friend] selftest: длинная протяжка не прошла:', err.message);
  }
  // Окно читаем не «сразу после 800 мс», а пока позиция не перестанет
  // меняться: протяжка — это ~120 асинхронных переездов через IPC, и на
  // загрузке системы очередь растягивается на секунды. Цепочка закончена,
  // когда окно стоит неподвижно; читать координаты раньше — значит мерить
  // середину переезда (замерено: 256 вместо 680, «окно не доехало»).
  let sizeAfter = win.getBounds();
  const settleFrom = Date.now();
  let lastPos = { x: sizeAfter.x, y: sizeAfter.y };
  let still = 0;
  // Неподвижность — ТРИ одинаковых снимка подряд (450 мс), а не один:
  // на загрузке системы очередь IPC растягивается, и один «стоящий» снимок
  // бывает между пачками переездов — выход по нему мерил середину пути.
  while (Date.now() - settleFrom < 9000) {
    await new Promise((r) => setTimeout(r, 150));
    const b = win.getBounds();
    if (b.x === lastPos.x && b.y === lastPos.y) {
      still++;
      if (still >= 3) break;
    } else {
      still = 0;
    }
    lastPos = { x: b.x, y: b.y };
  }
  sizeAfter = win.getBounds();
  const sizeHeld = Math.abs(sizeAfter.width - sizeBefore.width) <= 2 &&
    Math.abs(sizeAfter.height - sizeBefore.height) <= 2;
  // Курсор ушёл на 480,240 — и окно должно приехать примерно туда же: слишком
  // вольное «куда-нибудь уехало» пропустило бы и потерю движения, и рывок.
  const want = { x: 200 + 480, y: 300 + 240 };
  const off = { x: sizeAfter.x - want.x, y: sizeAfter.y - want.y };
  const dragAccurate = Math.abs(off.x) <= 24 && Math.abs(off.y) <= 24;
  console.log('[friend] selftest: протяжка 120 шагов — окно ' + sizeBefore.width + 'x' + sizeBefore.height +
    ' → ' + sizeAfter.width + 'x' + sizeAfter.height + ', позиция ' +
    sizeAfter.x + ',' + sizeAfter.y + ' (ожидалось ' + want.x + ',' + want.y +
    ', отклонение ' + off.x + ',' + off.y + ') → ' +
    (sizeHeld ? 'OK (размер не изменился)' : 'ОШИБКА (окно раздулось)') + '; ' +
    (dragAccurate ? 'OK (доехало за курсором)' : 'ОШИБКА (окно уехало не за курсором)'));

  // Третья часть: разговор. Клик по персонажу → ввод → реплика → ответ в
  // облачке. Ждём ЛЮБОЙ ответ сервера: её реплику или объяснение, почему её нет
  // (например, ключ не настроен). Так проверяется вся цепочка «клик → ввод →
  // канал → ответ → облачко» без живого ключа, а не только наличие файла.
  let chat = { ok: false, why: 'страница не ответила' };
  try {
    chat = await win.webContents.executeJavaScript(`(async () => {
      const app = window.friendApp;
      const chat = window.friendChat;
      if (!chat) return { ok: false, why: 'нет window.friendChat — скрипт разговора не подключился' };
      // Щёлкнем ровно по персонажу: проверяем настоящий путь «клик открывает
      // ввод», включая hitTest из drag.js.
      const rect = app.app.view.getBoundingClientRect();
      const cx = rect.left + app.avatar.container.x;
      const cy = rect.top + app.avatar.container.y;
      const opts = { bubbles: true, cancelable: true, pointerId: 3, isPrimary: true, buttons: 1, clientX: cx, clientY: cy, screenX: cx, screenY: cy };
      document.getElementById('stage').dispatchEvent(new PointerEvent('pointerdown', opts));
      window.dispatchEvent(new PointerEvent('pointerup', opts));
      await new Promise((r) => setTimeout(r, 200));
      const field = document.querySelector('.chat-input textarea');
      if (!field || !chat.isOpen()) return { ok: false, why: 'клик по персонажу не открыл окно ввода' };
      field.value = 'проверка связи';
      field.dispatchEvent(new Event('input', { bubbles: true }));
      document.querySelector('.chat-input').dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
      await new Promise((r) => setTimeout(r, 6000));
      const bubble = document.querySelector('.bubble');
      const textEl = bubble ? bubble.querySelector('.bubble-text') : null;
      const text = textEl ? textEl.textContent : '';
      const shown = !!(bubble && !bubble.hidden && text);
      // Заодно ширину поля ввода: «бар на весь экран» начинался именно с
      // раздувшегося окна, а поле ввода тянулось за ним.
      const input = document.querySelector('.chat-input');
      return {
        ok: shown,
        why: shown ? (bubble.className + ': ' + text) : 'облачко осталось пустым — ответ сервера не дошёл',
        inputW: input ? Math.round(input.getBoundingClientRect().width) : 0,
        uiW: app.windowSize().w,
      };
    })()`);
  } catch (err) {
    console.error('[friend] selftest: разговор не проверился:', err.message);
  }
  // Поле ввода не должно быть шире персонажа с его полями: иначе оно тянется на
  // весь экран и закрывает человеку работу под ним.
  const inputOk = chat.inputW > 0 && chat.inputW <= chat.uiW - 8;
  chat.ok = chat.ok && inputOk;
  console.log('[friend] selftest: разговор — ' + (chat.ok ? 'OK (' + chat.why + ')' : 'ОШИБКА (' + chat.why + ')') +
    '; поле ввода ' + chat.inputW + ' px при окне ' + chat.uiW + ' px → ' +
    (inputOk ? 'OK (не шире персонажа)' : 'ОШИБКА (растянулось шире персонажа)'));

  // Пятая часть: поглаживание по голове. Водим мышью по голове две с половиной
  // секунды и ждём сердечки и улыбку. Глазами такой эффект не проверить: он
  // тает за полторы секунды, поэтому считаем сами объекты.
  let pet = { ok: false, why: 'страница не ответила' };
  try {
    pet = await win.webContents.executeJavaScript(`(async () => {
      const app = window.friendApp;
      const avatar = app && app.avatar;
      if (!avatar || !app.pet) return { ok: false, why: 'нет друга/сторожа поглаживания' };
      const rect = app.app.view.getBoundingClientRect();
      const head = avatar.headRect();
      const stage = document.getElementById('stage');
      const t0 = performance.now();
      let i = 0;
      while (performance.now() - t0 < 2600) {
        i++;
        const cx = rect.left + avatar.container.x + (head.x0 + head.x1) / 2;
        const cy = rect.top + avatar.container.y + (head.y0 + head.y1) / 2;
        stage.dispatchEvent(new PointerEvent('pointermove', {
          bubbles: true,
          clientX: cx + Math.sin(i / 3) * 34,
          clientY: cy + Math.cos(i / 4) * 10,
        }));
        await new Promise((r) => setTimeout(r, 16));
      }
      const hearts = avatar.hearts ? avatar.hearts() : -1;
      const emotion = avatar.emotion();
      const ok = hearts > 0 && emotion === 'happy';
      return {
        ok: ok,
        why: 'движений ' + i + ', сердечек ' + hearts + ', лицо ' + emotion +
          '; прогресс ' + Math.round(app.pet && app.pet.progress ? app.pet.progress() : -1) +
          ' мс, волочение ' + (app.drag && app.drag.isDragging ? app.drag.isDragging() : '?') +
          '; голова ' + JSON.stringify(avatar.headRect ? avatar.headRect() : null) +
          '; точка ' + Math.round(avatar.container.x) + ',' + Math.round(avatar.container.y),
      };
    })()`);
  } catch (err) {
    console.error('[friend] selftest: поглаживание не проверилось:', err.message);
  }
  console.log('[friend] selftest: поглаживание по голове — ' +
    (pet.ok ? 'OK (' + pet.why + ')' : 'ОШИБКА (' + pet.why + ')'));

  // Шестая часть: сон и «Zzz». Засыпание по идл-событию — из этапа 4, и глаза
  // его не поймают: буквы живут по два с лишним секунды и появляются раз в
  // полторы, поэтому считаем объекты, как с сердечками. Проверяем и то, что
  // сон не будит себя: после «Zzz» лицо должно остаться закрытым.
  let sleep = { ok: false, why: 'страница не ответила' };
  try {
    sleep = await win.webContents.executeJavaScript(`(async () => {
      const avatar = window.friendApp && window.friendApp.avatar;
      if (!avatar || !avatar.zzz) return { ok: false, why: 'нет друга/счётчика Zzz' };
      avatar.setEmotion('sleep');
      let z = 0;
      const t0 = performance.now();
      // Ждём, пока поток «Z» успеет породить хотя бы одну букву.
      while (performance.now() - t0 < 2500) {
        z = avatar.zzz();
        if (z > 0) break;
        await new Promise((r) => setTimeout(r, 100));
      }
      // say() во сне не должен открывать глаза.
      avatar.say('спокойной ночи');
      await new Promise((r) => setTimeout(r, 300));
      const asleep = avatar.emotion() === 'sleep';
      // Пробуждаем обратно: дальше по самопроверке идут разделы про бодрствующую.
      avatar.setEmotion('calm');
      const woke = avatar.emotion() === 'calm';
      return {
        ok: z > 0 && asleep && woke,
        why: 'Z ' + z + ', сон ' + asleep + ', пробуждение ' + woke,
      };
    })()`);
  } catch (err) {
    console.error('[friend] selftest: сон не проверился:', err.message);
  }
  console.log('[friend] selftest: сон и «Zzz» — ' +
    (sleep.ok ? 'OK (' + sleep.why + ')' : 'ОШИБКА (' + sleep.why + ')'));

  // Седьмая часть: настроение видна на лице (этап 5). Полоска low делает
  // лицо грустным, high — лучезарным, mid — спокойным; сон и «думает» полоску
  // не перебивают, а «лицо покоя» после событий возвращается к настроению.
  let mood = { ok: false, why: 'страница не ответила' };
  try {
    mood = await win.webContents.executeJavaScript(`(async () => {
      const avatar = window.friendApp && window.friendApp.avatar;
      if (!avatar || !window.friendApp.applyMood) return { ok: false, why: 'нет друга/applyMood' };
      avatar.setEmotion('calm');
      window.friendApp.applyMood({ mood: 0.2, energy: 0.5, affection: 0.5, band: 'low' });
      const lowFace = avatar.emotion();
      window.friendApp.applyMood({ mood: 0.9, energy: 0.5, affection: 0.5, band: 'high' });
      const highFace = avatar.emotion();
      // Настроение не перебивает сон: спящую полоска не будит.
      avatar.setEmotion('sleep');
      window.friendApp.applyMood({ mood: 0.9, band: 'high' });
      const asleepStays = avatar.emotion() === 'sleep';
      avatar.setEmotion('calm');
      // «Лицо покоя» после событий — полоска, а не дежурное спокойствие.
      const face = avatar.calmFace();
      window.friendApp.applyMood({ mood: 0.5, band: 'mid' });
      avatar.setEmotion('calm');
      const midFace = avatar.calmFace();
      return {
        ok: lowFace === 'sad' && highFace === 'happy' && asleepStays && face === 'happy' && midFace === 'calm',
        why: 'low→' + lowFace + ', high→' + highFace + ', сон не перебит ' + asleepStays +
          ', лицо покоя ' + face + '/' + midFace,
      };
    })()`);
  } catch (err) {
    console.error('[friend] selftest: настроение не проверилось:', err.message);
  }
  console.log('[friend] selftest: настроение на лице — ' +
    (mood.ok ? 'OK (' + mood.why + ')' : 'ОШИБКА (' + mood.why + ')'));

  // Восьмая часть: прогулка (этап 6). Заставляем её шагать вправо и смотрим,
  // что окно ЕДЕТ по столу, а не только перебирает ногами на месте. Потом
  // разворот (walk(-1)) и остановка.
  let walk = { ok: false, why: 'страница не ответила' };
  try {
    const x0 = win.getPosition()[0];
    walk = await win.webContents.executeJavaScript(`(async () => {
      const app = window.friendApp;
      if (!app || !app.startWalk) return { ok: false, why: 'нет прогулки на странице' };
      app.startWalk(1);
      const dir = app.walking();
      await new Promise((r) => setTimeout(r, 1300));
      const x1 = (await app.bridge.getPosition()).x;
      app.startWalk(-1);
      const back = app.walking();
      app.stopWalk('самопроверка');
      const stopped = app.walking();
      return {
        ok: dir === 1 && back === -1 && stopped === 0,
        why: 'шагает ' + dir + ', разворот ' + back + ', после стоп ' + stopped +
          ', окно на странице ' + x1,
      };
    })()`);
    await new Promise((r) => setTimeout(r, 200));
    const x1 = win.getPosition()[0];
    walk.ok = walk.ok && Math.abs(x1 - x0) >= 20;
    walk.why += ', окно сместилась на ' + (x1 - x0) + ' px';
  } catch (err) {
    console.error('[friend] selftest: прогулка не проверилась:', err.message);
  }
  console.log('[friend] selftest: прогулка — ' +
    (walk.ok ? 'OK (' + walk.why + ')' : 'ОШИБКА (' + walk.why + ')'));

  // Девятая часть: реакция на клик фразой (этап 6). Нажать на неё и отпустить,
  // не сдвигая мышь, — это клик: она отвечает короткой локальной фразой (без
  // ключа модели). Кликаем СПЯЩЕЙ — так одной проверкой покрываем и фразу, и
  // пробуждение кликом. Клик уходит в цепочку drag.js асинхронно (после
  // выравнивания и чтения позиции по IPC), поэтому облачко ждём с таймаутом.
  let poke = { ok: false, why: 'страница не ответила' };
  try {
    poke = await win.webContents.executeJavaScript(`(async () => {
      const app = window.friendApp;
      const avatar = app && app.avatar;
      const chat = app && app.chat;
      if (!avatar || !chat) return { ok: false, why: 'нет друга/разговора' };
      const rect = app.app.view.getBoundingClientRect();
      const head = avatar.headRect();
      const cx = rect.left + avatar.container.x + (head.x0 + head.x1) / 2;
      const cy = rect.top + avatar.container.y + (head.y0 + head.y1) / 2;
      const stage = document.getElementById('stage');
      const hit = avatar.hitTest(cx - rect.left - avatar.container.x, cy - rect.top - avatar.container.y);
      avatar.setEmotion('sleep');
      chat.hideBubble();   // старая строка разговора не должна попасть в замер
      stage.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true, pointerId: 1, clientX: cx, clientY: cy }));
      const grabbing = app.drag && app.drag.isDragging();
      window.dispatchEvent(new PointerEvent('pointerup', { bubbles: true, pointerId: 1, clientX: cx, clientY: cy }));
      let phrase = '';
      const t0 = performance.now();
      while (performance.now() - t0 < 4000) {
        const box = document.querySelector('.bubble');
        const t = box && !box.hidden ? box.textContent.trim() : '';
        if (t) { phrase = t; break; }
        await new Promise((r) => setTimeout(r, 100));
      }
      // Цепочка клика асинхронная — даём ей дойти до конца перед чтением лица.
      await new Promise((r) => setTimeout(r, 300));
      const awake = avatar.emotion() !== 'sleep';
      const opened = chat.isOpen();
      // Клик открывает окно ввода — закрываем обратно, дальше идут разделы про
      // спящую и про меню.
      if (opened) chat.close();
      return {
        ok: phrase.length > 0 && awake,
        why: 'фраза «' + phrase + '», пробуждение ' + awake + ', попадание ' + hit +
          ', взяли ' + grabbing + ', ввод открыт ' + opened,
      };
    })()`);
  } catch (err) {
    console.error('[friend] selftest: клик фразой не проверился:', err.message);
  }
  console.log('[friend] selftest: реакция на клик фразой — ' +
    (poke.ok ? 'OK (' + poke.why + ')' : 'ОШИБКА (' + poke.why + ')'));

  // Четвёртая часть: сохранённая позиция за пределами экрана. Такое бывает от
  // отключённого монитора, смены разрешения или битых координат в памяти окна.
  // Проверяем две вещи: окно возвращается на экран И сервер получает настоящее
  // место. Второе важнее: без него окно уезжает в никуда при каждом запуске, и
  // достать его мышью уже нельзя — ровно то, на что жалуются живые люди.
  let healed = null;
  try {
    healed = await win.webContents.executeJavaScript(`(async () => {
      const app = window.friendApp;
      // Моделируем память окна: точка далеко за пределами любого экрана.
      const real = await app.bridge.setPosition(-26215, 320);
      await new Promise((r) => setTimeout(r, 500));
      return { real: real };
    })()`);
  } catch (err) {
    console.error('[friend] selftest: проверка позиции за краем экрана не прошла:', err.message);
  }

  const onScreen = (p, bounds) => screen.getAllDisplays().some((d) => {
    const a = d.workArea;
    const w = bounds ? bounds.width : 320;
    const h = bounds ? bounds.height : 420;
    return Math.min(p.x + w, a.x + a.width) - Math.max(p.x, a.x) >= MIN_VISIBLE &&
      Math.min(p.y + h, a.y + a.height) - Math.max(p.y, a.y) >= MIN_VISIBLE;
  });
  const windowVisible = onScreen({ x: win.getBounds().x, y: win.getBounds().y }, win.getBounds());
  const saved = await serverPosition();
  const savedVisible = !!saved && onScreen(saved, null);
  console.log('[friend] selftest: позиция за краем экрана — запросили -26215,320, окно ' +
    win.getBounds().x + ',' + win.getBounds().y +
    ' → ' + (windowVisible ? 'OK (на экране)' : 'ОШИБКА (окно уехало за пределы экрана)') +
    '; сервер помнит ' + (saved ? saved.x + ',' + saved.y : '?') +
    ' → ' + (savedVisible ? 'OK (память исправлена)' : 'ОШИБКА (в памяти остался адрес за экраном)'));

  // Седьмая часть: меню по правому клику, размер и посадка на панель задач.
  //
  // Это проверяется машиной, а не глазами, по двум причинам. Первая: посадка —
  // это ОДНО ЧИСЛО (нижняя кромка рабочей области), и глазами легко принять
  // «примерно там же» за правильное место, а на другом мониторе или панели
  // высотой 40 она окажется в воздухе или утопленной в панель. Вторая: меню и
  // ползунок — это механика окна (расширение под меню, resize под новый размер
  // спрайта), и именно такие цепочки ломаются молча.
  let menuTest = { ok: false, why: 'страница не ответила' };
  try {
    const area = screen.getDisplayMatching(win.getBounds()).workArea;
    menuTest = await win.webContents.executeJavaScript(`(async () => {
      const app = window.friendApp;
      if (!app || !app.menu) return { ok: false, why: 'нет меню или страницы' };
      const rect = app.app.view.getBoundingClientRect();
      const cx = rect.left + app.avatar.container.x;
      const cy = rect.top + app.avatar.container.y;
      // Правый клик ИМЕННО по персонажу: окно шире его, и меню в пустом месте
      // отнимало бы клик у рабочего стола.
      window.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true, clientX: cx, clientY: cy, button: 2 }));
      await new Promise((r) => setTimeout(r, 500));
      const panel = document.querySelector('.menu');
      const opened = app.menu.isOpen() && panel && !panel.hidden;
      const panelFits = panel ? panel.getBoundingClientRect().width > 100 : false;
      // Размер — ползунком, как это делает человек.
      const slider = panel ? panel.querySelector('input[type=range]') : null;
      if (!slider) return { ok: false, why: 'в меню нет ползунка размера' };
      const before = app.avatar.width;
      slider.value = '8';
      slider.dispatchEvent(new Event('input', { bubbles: true }));
      await new Promise((r) => setTimeout(r, 700));
      const scaled = app.avatar.scale() > 1.2 && app.avatar.width > before;
      // Посадка на панель задач.
      await app.applySeat('taskbar');
      await new Promise((r) => setTimeout(r, 500));
      const anchor = app.anchor();
      const pose = app.avatar.pose();
      slider.value = '5';
      slider.dispatchEvent(new Event('input', { bubbles: true }));
      await new Promise((r) => setTimeout(r, 700));
      await app.applySeat('free');
      await new Promise((r) => setTimeout(r, 300));
      app.menu.close();
      await new Promise((r) => setTimeout(r, 300));
      return {
        ok: opened && panelFits && scaled && pose === 'sit',
        why: 'меню ' + (opened ? 'открылось' : 'НЕ открылось') +
          (panelFits ? '' : '(панель пустая)') +
          ', размер ' + (scaled ? 'вырос' : 'НЕ вырос') +
          ', поза ' + pose + ', якорь y=' + Math.round(anchor.y),
        anchorY: Math.round(anchor.y),
        workAreaBottom: 0,
        // Размер, который в итоге стоит в настройках меню: по нему проверяем, что
        // выбор доехал до ФАЙЛА, а не остался в памяти страницы.
        pix: app.menu.settings().pix,
      };
    })()`);
    // Место посадки сверяем НА СТОРОНЕ ОБОЛОЧКИ: только она знает рабочую область
    // (у Windows она без панели задач), поэтому именно здесь видно, села ли она
    // на панель или повисла в воздухе.
    if (menuTest && typeof menuTest.anchorY === 'number') {
      const wantY = area.y + area.height;
      const onTaskbar = Math.abs(menuTest.anchorY - wantY) <= 2;
      menuTest.ok = menuTest.ok && onTaskbar;
      menuTest.why += '; посадка ' + (onTaskbar ? 'OK (кромка панели ' + wantY + ')' :
        'ОШИБКА (ожидалась кромка ' + wantY + ', а она на ' + menuTest.anchorY + ')');

      // Посадка у края экрана и выход из-за него.
      //
      // Проверяем числами, а не «похоже на правду»: кромка окна стоит РОВНО на
      // краю экрана (иначе она выглядывает не из-за края, а из воздуха); в
      // спрятанном виде спрайт целиком за кромкой окна — значит его не видно; а
      // вышедшая — целиком внутри окна, то есть не обрезана. И отдельно — что
      // выход ПЛАВНЫЙ: за секунду его положение меняется десятки раз, а не
      // прыгает одним шагом (раньше «у края» была просто неподвижная полоса
      // спрайта — ровно то, на что жаловался человек).
      const leftPeek = await win.webContents.executeJavaScript(`(async () => {
        const app = window.friendApp;
        const spriteW = app.avatar.width;
        const shot = () => {
          const x = app.avatar.container.x;
          return { left: Math.round(x - spriteW / 2), right: Math.round(x + spriteW / 2) };
        };
        const wait = (ms) => new Promise((r) => setTimeout(r, ms));
        await app.applySeat('edge-left');
        await wait(500);
        const dir = app.peek.dir();
        // Меряем СРАЗУ после переключения (applyPeek считает смещение на месте):
        // иначе между переключением и замером успевает пройти кадр, а заодно
        // может вмешаться живая мышь — у края экрана она сама вызывает «покажись»,
        // и правильная поза превратилась бы в случайную.
        app.peek.snap(0);
        app.applyPeek();
        const hidden = shot();
        app.peek.snap(1);
        app.applyPeek();
        const shown = shot();
        app.peek.snap(0);
        app.peek.poke();
        const seen = new Set();
        for (let i = 0; i < 70; i++) {
          await new Promise((r) => requestAnimationFrame(r));
          seen.add(Math.round(app.avatar.container.x));
        }
        return {
          pose: app.avatar.pose(), dir: dir, steps: seen.size,
          canvasW: Math.round(app.app.view.getBoundingClientRect().width),
          hidden: hidden, shown: shown,
        };
      })()`);
      // Кромку левого окна читаем СРАЗУ после левой постановки: дальше окно уже
      // у правого края, и в сообщении была бы чужая цифра.
      const lb = win.getBounds();
      const leftFlush = Math.abs(lb.x - area.x) <= 2;
      // Спрятана: спрайт целиком ЛЕВЕЕ окна — картинка срезает его по кромке,
      // значит ни на экране, ни на соседнем мониторе его не видно.
      const leftHiddenOk = leftPeek.hidden.right <= 0;
      // Вышла: спрайт целиком в окне, а окно стоит кромкой к краю экрана — значит
      // она и вся на экране, и не срезана.
      const leftShownOk = leftPeek.shown.left >= 0 && leftPeek.shown.right <= leftPeek.canvasW;

      const rightPeek = await win.webContents.executeJavaScript(`(async () => {
        const app = window.friendApp;
        const spriteW = app.avatar.width;
        const wait = (ms) => new Promise((r) => setTimeout(r, ms));
        await app.applySeat('edge-right');
        await wait(500);
        const dir = app.peek.dir();
        app.peek.snap(0);
        app.applyPeek();
        const hidden = { left: Math.round(app.avatar.container.x - spriteW / 2) };
        app.peek.snap(1);
        app.applyPeek();
        const shown = Math.round(app.avatar.container.x + spriteW / 2);
        return {
          pose: app.avatar.pose(), dir: dir, hidden: hidden, shown: shown,
          canvasW: Math.round(app.app.view.getBoundingClientRect().width),
        };
      })()`);
      const rb = win.getBounds();
      const rightFlush = Math.abs((rb.x + rb.width) - (area.x + area.width)) <= 2;
      const rightHiddenOk = rightPeek.hidden.left >= rightPeek.canvasW;
      const rightShownOk = rightPeek.shown <= rightPeek.canvasW;
      const peekOk = leftPeek.dir === -1 && rightPeek.dir === 1 &&
        leftPeek.pose === 'stand' && rightPeek.pose === 'stand' &&
        leftFlush && rightFlush && leftHiddenOk && leftShownOk &&
        rightHiddenOk && rightShownOk && leftPeek.steps >= 5;
      menuTest.ok = menuTest.ok && peekOk;
      menuTest.why += '; у левого края ' + (leftFlush && leftHiddenOk && leftShownOk ?
        'OK (кромка окна ' + lb.x + ' = край экрана ' + area.x +
        ', спрятана до ' + leftPeek.hidden.right + ' px, вышла на ' + leftPeek.shown.left +
        '..' + leftPeek.shown.right + ' в окне ' + leftPeek.canvasW + ')' :
        'ОШИБКА (кромка ' + lb.x + ' против края ' + area.x +
        ', спрятана ' + JSON.stringify(leftPeek.hidden) + ', вышла ' + JSON.stringify(leftPeek.shown) + ')');
      menuTest.why += '; у правого края ' + (rightFlush && rightHiddenOk && rightShownOk ?
        'OK (спрятана от ' + rightPeek.hidden.left + ' при окне ' + rightPeek.canvasW +
        ', вышла до ' + rightPeek.shown + ')' :
        'ОШИБКА (кромка ' + (rb.x + rb.width) + ' против ' + (area.x + area.width) +
        ', спрятана ' + rightPeek.hidden.left + ' при окне ' + rightPeek.canvasW + ')');
      menuTest.why += '; выход ' + (leftPeek.steps >= 5 ? 'плавный OK (' + leftPeek.steps +
        ' положений за 1.2 с)' : 'ОШИБКА (положений ' + leftPeek.steps +
        ' — двигается рывком или не двигается вовсе)');

      // Облачко длинной реплики: не растёт вместе с текстом и не тянет за собой
      // окно. Именно на это жалуются живые люди ("облачко на пол экрана").
      const bubble = await win.webContents.executeJavaScript(`(async () => {
        const app = window.friendApp;
        const size = app.windowSize();
        app.chat.showLine('her', 'проверка длинной реплики. '.repeat(80));
        await new Promise((r) => setTimeout(r, 300));
        const el = document.querySelector('.bubble');
        const r = el.getBoundingClientRect();
        const pad = parseFloat(getComputedStyle(document.documentElement).getPropertyValue('--pad-top')) || 0;
        const after = app.windowSize();
        // Все размеры снимаем ДО того, как облачко спрячется: у скрытого
        // элемента и scrollHeight, и clientHeight равны нулю, и проверка
        // «текст прокручивается» превратилась бы в проверку «элемента нет».
        const out = {
          h: Math.round(r.height),
          pad: Math.round(pad),
          scrolls: el.scrollHeight > el.clientHeight + 1,
          grew: after.w !== size.w || after.h !== size.h,
        };
        app.chat.hideBubble();
        return out;
      })()`);
      const bubbleOk = bubble && bubble.h <= bubble.pad + 20 && bubble.scrolls && !bubble.grew;
      menuTest.ok = menuTest.ok && bubbleOk;
      menuTest.why += '; облачко ' + (bubbleOk ? 'OK (' + bubble.h + ' px при поле ' + bubble.pad +
        ', прокручивается)' : 'ОШИБКА (высота ' + (bubble && bubble.h) + ' при поле ' +
        (bubble && bubble.pad) + ', прокрутка ' + (bubble && bubble.scrolls) +
        ', окно выросло: ' + (bubble && bubble.grew) + ')');

      await win.webContents.executeJavaScript(`window.friendApp.applySeat('free')`).catch(() => {});
      await new Promise((r) => setTimeout(r, 300));

      // Настройки должны доехать до ФАЙЛА, а не только до хранилища страницы:
      // файл — то единственное, что переживает запуск (start.bat гасит прошлое
      // окно силой, и localStorage не успевает сброситься на диск). Без этой
      // проверки посадка «у края» терялась бы после каждого перезапуска — именно
      // так и выглядело «выхода из-за края нет».
      // Файл спросим «как есть» (readSettingsRaw): если его нет, readSettings молча
      // вернёт значения по умолчанию, и проверка прошла бы на пустом месте.
      const savedFile = readSettingsRaw();
      const settingsOk = !!savedFile && savedFile.pix === menuTest.pix &&
        typeof savedFile.seat === 'string' && typeof savedFile.keepTop === 'boolean' &&
        typeof savedFile.follow === 'number';
      menuTest.ok = menuTest.ok && settingsOk;
      menuTest.why += '; настройки в файле ' + JSON.stringify(savedFile) +
        ' (в меню ' + menuTest.pix + ') → ' + (settingsOk ? 'OK' :
        'ОШИБКА (настройки не доехали до файла — посадка и размер теряются при запуске)');
    }
  } catch (err) {
    console.error('[friend] selftest: меню и посадка не проверились:', err.message);
    menuTest = { ok: false, why: err.message };
  }
  console.log('[friend] selftest: меню, размер и посадка — ' +
    (menuTest.ok ? 'OK (' + menuTest.why + ')' : 'ОШИБКА (' + menuTest.why + ')'));

  // Восьмая часть: понимание окна. Проверяем два независимых факта:
  //   1) помощник выдал ХОТЯ БЫ ОДНУ строку — связка с Windows API (активное
  //      окно, имя процесса) жива на этой машине;
  //   2) цепочка страница → сервер проходит: отправляем странице событие того
  //      же формата, что присылает помощник, и сервер должен запомнить
  //      приложение по /healthz. Страница в ответе не участвует — её обработчик
  //      обычный, проверка меряет всю линию целиком.
  let focusChain = { ok: false, why: 'не проверено' };
  {
    const helperOk = process.platform !== 'win32' || focusLines > 0;
    const sent = { process: 'Code.exe', title: 'selftest — проверка цепочки' };
    if (win && !win.isDestroyed()) win.webContents.send('focus:info', sent);
    let got = null;
    const from = Date.now();
    while (Date.now() - from < 8000) {
      await new Promise((r) => setTimeout(r, 250));
      const f = await serverFocus();
      if (f && f.process === sent.process) { got = f; break; }
    }
    // Строка тестового окна посадки (та, что гоняет 16-я часть) не имеет
    // права менять контекст рабочей Миры: сервер её отбрасывает по маркеру.
    if (win && !win.isDestroyed()) {
      win.webContents.send('focus:info', { process: 'SelftestTarget.exe', title: 'SELFTEST-окно-посадки', idle: 1 });
    }
    let afterTest = null;
    const fromTest = Date.now();
    while (Date.now() - fromTest < 4000) {
      await new Promise((r) => setTimeout(r, 250));
      const f = await serverFocus();
      if (f && f.process === 'SelftestTarget.exe') { afterTest = f; break; }
    }
    const testDropped = !afterTest;
    focusChain = {
      ok: helperOk && !!got && testDropped,
      why: 'строк от помощника ' + focusLines + (helperOk ? '' : ' (focuswatch не запущен)') +
        '; сервер помнит ' + (got ? got.process + ' (' + got.kind + ')' : 'ничего') +
        '; тестовое окно отброшено ' + (testDropped ? 'OK' : 'ОШИБКА (сервер принял тестовое окно)'),
    };
  }
  console.log('[friend] selftest: понимание окна — ' +
    (focusChain.ok ? 'OK (' + focusChain.why + ')' : 'ОШИБКА (' + focusChain.why + ')'));

  // Шестая часть: «персонаж исчез, а окно и процесс живы». Именно так выглядит
  // самая неприятная поломка: подключение есть, сторож экрана молчит, а на
  // картинке ничего не меняется. Воспроизводим её честно — обрываем отрисовку
  // (так это делает потеря контекста WebGL или подвисший графический процесс) и
  // ждём, что оболочка заметит молчание пульса и поднимет страницу заново.
  // Проверяем ДВА факта: страница действительно перезагрузилась (метка
  // window.friendAlive пережила только перезагрузку) и кадры снова идут (пульс
  // свежий).
  let vanish = { ok: false, why: 'страница не ответила' };
  try {
    const stopped = await win.webContents.executeJavaScript(`(() => {
      const app = window.friendApp;
      if (!app) return false;
      window.friendAlive = false;   // эту метку перезагрузка вернёт в true
      app.app.ticker.stop();        // кадры прекратились — пульс замолчал
      return true;
    })()`);
    if (!stopped) {
      vanish = { ok: false, why: 'нет window.friendApp — страница не собралась' };
    } else {
      const from = Date.now();
      let alive = false;
      while (Date.now() - from < 25000) {
        await new Promise((r) => setTimeout(r, 500));
        try {
          alive = await win.webContents.executeJavaScript(
            'window.friendAlive === true && !!(window.friendApp && window.friendApp.app.ticker.started && window.friendApp.avatar)',
          );
        } catch (_err) {
          // Страница перезагружается — executeJavaScript в этот момент бросает.
          // Это ожидаемая часть лечения, а не ошибка проверки.
          alive = false;
        }
        if (alive) break;
      }
      await new Promise((r) => setTimeout(r, 1500));
      const beatAge = Date.now() - lastBeat;
      const seconds = Math.round((Date.now() - from) / 1000);
      vanish = {
        ok: alive && beatAge < 2000,
        why: alive
          ? 'вернулась за ' + seconds + ' с, пульс ' + beatAge + ' мс назад'
          : 'за 25 с так и не вернулась',
      };
    }
  } catch (err) {
    console.error('[friend] selftest: проверка «исчезла» не прошла:', err.message);
    vanish = { ok: false, why: err.message };
  }
  console.log('[friend] selftest: персонаж перестал рисоваться — ' +
    (vanish.ok ? 'OK (' + vanish.why + ')' : 'ОШИБКА (' + vanish.why + ')'));

  // Одиннадцатая часть: внешний лист кадров (этап 6, смена спрайтов).
  // Подкладываем в папку арта (ту, что сервер раздаёт по /sprites/) лист
  // однотонных PNG, перегружаем страницу и смотрим, что она живёт по кадрам
  // листа: кадр следует за эмоцией, речью, взмахом, шагом (оба кадра +
  // зеркало) и морганием. Потом арт убираем, перегружаем — и проверяем, что
  // вернулась нарисованная кодом Мира: отсутствие папки с артом не должно
  // ломать персонажа.
  let sheet = { ok: false, why: 'страница не ответила' };
  try {
    const fs = require('fs');
    const artDir = require('path').join(process.env.APPDATA, 'Friend', 'sprites');
    fs.mkdirSync(artDir, { recursive: true });
    // Кадры рисуем самой страницей (canvas → PNG), чтобы самопроверке не
    // пришлось везти бинарные литеры чужого арта.
    const imgs = await win.webContents.executeJavaScript(`(async () => {
      const mk = (color) => {
        const c = document.createElement('canvas');
        c.width = 40; c.height = 56;
        const x = c.getContext('2d');
        x.fillStyle = color; x.fillRect(0, 0, 40, 56);
        return c.toDataURL('image/png').split(',')[1];
      };
      return {
        stand: mk('#204080'), sit: mk('#402060'),
        walk1: mk('#20a040'), walk2: mk('#20c0c0'),
        happy: mk('#d0a020'), sad: mk('#606060'),
        think: mk('#804020'), sleep: mk('#303040'),
        talk: mk('#c03030'), blink: mk('#101018'), wave: mk('#30c0c0'),
      };
    })()`);
    fs.writeFileSync(require('path').join(artDir, 'sheet.json'), JSON.stringify({
      frames: {
        stand: 'stand.png', sit: 'sit.png', walk1: 'walk1.png', walk2: 'walk2.png',
        happy: 'happy.png', sad: 'sad.png', think: 'think.png', sleep: 'sleep.png',
        talk: 'talk.png', blink: 'blink.png', wave: 'wave.png',
      },
    }));
    for (const name of Object.keys(imgs)) {
      fs.writeFileSync(require('path').join(artDir, name + '.png'), Buffer.from(imgs[name], 'base64'));
    }

    // Страница грузит лист сама, при заведении. Перезагружаем — и ждём, пока
    // она окажется на кадрах листа.
    win.webContents.reload();
    const waitForSheet = async (wantSheet, budgetMs) => {
      const from = Date.now();
      while (Date.now() - from < budgetMs) {
        await new Promise((r) => setTimeout(r, 300));
        try {
          const v = await win.webContents.executeJavaScript(`(async () => {
            const a = window.friendApp && window.friendApp.avatar;
            if (!a || !a.usingSheet) return null;
            try { await a.sheetReady; } catch (e) { return null; }
            return a.usingSheet();
          })()`);
          if (v === true && wantSheet) return true;
          if (v === false && !wantSheet) return true;
        } catch (_err) {
          // страница перезагружается — это ожидаемая часть процедуры
        }
      }
      return false;
    };
    const onSheet = await waitForSheet(true, 25000);

    const frames = onSheet
      ? await win.webContents.executeJavaScript(`(async () => {
        const a = window.friendApp.avatar;
        const nap = (ms) => new Promise((r) => setTimeout(r, ms));
        const seen = {};
        const mark = () => { const f = a.currentFrame(); if (f) seen[f] = true; return f; };
        const out = {};
        a.setMoodBand('mid');
        a.setEmotion('calm');
        a.setPose('stand');
        a.setWalk(0);
        out.stand = mark();
        a.setEmotion('happy'); out.happy = mark();
        a.setEmotion('sleep'); out.sleep = mark();
        a.setEmotion('calm');
        a.say('привет, это проверка листа');
        for (let i = 0; i < 40 && !seen.talk; i++) { await nap(50); mark(); }
        out.talk = !!seen.talk;
        a.wave();
        for (let i = 0; i < 40 && !seen.wave; i++) { await nap(50); mark(); }
        out.wave = !!seen.wave;
        a.setWalk(1);
        for (let i = 0; i < 40 && !(seen.walk1 && seen.walk2); i++) { await nap(50); mark(); }
        out.walk = !!seen.walk1 && !!seen.walk2;
        a.setWalk(-1);
        await nap(350);
        out.mirror = a.mirrored();
        a.setWalk(0);
        // Моргание начинается через 2.5–6 с после заведения — ждём с запасом.
        for (let i = 0; i < 170 && !seen.blink; i++) { await nap(50); mark(); }
        out.blink = !!seen.blink;
        a.setEmotion('calm');
        return out;
      })()`).catch(() => null)
      : null;

    // Убираем арт — и перегружаем: без папки с кадрами персонаж обязан
    // вернуться на процедурную отрисовку.
    fs.rmSync(artDir, { recursive: true, force: true });
    win.webContents.reload();
    const backProcedural = await waitForSheet(false, 25000);

    const f = frames || {};
    sheet.ok = onSheet && f.stand === 'stand' && f.happy === 'happy' && f.sleep === 'sleep' &&
      f.talk === true && f.wave === true && f.walk === true && f.mirror === true &&
      f.blink === true && backProcedural;
    sheet.why = 'на листе ' + onSheet +
      ' (stand ' + f.stand + ', happy ' + f.happy + ', sleep ' + f.sleep +
      ', речь ' + f.talk + ', взмах ' + f.wave + ', шаг ' + f.walk +
      ', зеркало ' + f.mirror + ', моргание ' + f.blink + ')' +
      ', без арта снова нарисована ' + backProcedural;
  } catch (err) {
    console.error('[friend] selftest: внешний лист не проверился:', err.message);
    sheet = { ok: false, why: err.message };
  }
  console.log('[friend] selftest: внешний лист кадров — ' +
    (sheet.ok ? 'OK (' + sheet.why + ')' : 'ОШИБКА (' + sheet.why + ')'));

  // Двенадцатая часть: озвучка реплик (этап 7, Edge TTS). Проверяем три вещи:
  // сервер сообщает доступность голоса в welcome; /tts по тексту отдаёт mp3,
  // который страница реально слушает (длительность > 1 с); и локальный путь
  // кэша. В облако Microsoft при этом уходит только текст реплики — это
  // зафиксированная граница приватности функции.
  let voice = { ok: false, why: 'страница не ответила' };
  try {
    const tts = await win.webContents.executeJavaScript(`(async () => {
      const info = window.friendApp && window.friendApp.voiceInfo && window.friendApp.voiceInfo();
      if (!info || !info.available) return { ok: false, why: 'озвучка не заявлена сервером' };
      const text = 'Проверка озвучки. ' + Date.now();
      const res = await fetch('/tts?text=' + encodeURIComponent(text));
      if (!res.ok) return { ok: false, why: '/tts статус ' + res.status };
      const blob = await res.blob();
      const url = URL.createObjectURL(blob);
      const dur = await new Promise((resolve) => {
        const a = new Audio();
        a.src = url;
        const done = (d) => { URL.revokeObjectURL(url); resolve(d); };
        a.addEventListener('loadedmetadata', () => done(a.duration));
        a.addEventListener('error', () => done(-1));
        setTimeout(() => done(a.duration || -1), 5000);
      });
      return { ok: dur >= 1, dur: dur, bytes: blob.size, voice: info.voice };
    })()`);
    // Путь кэша озвучки — локальный (папка данных рядом с памятью).
    const fs = require('fs');
    const path = require('path');
    const ttsDir = path.join(process.env.APPDATA, 'Friend', 'tts');
    const cached = fs.existsSync(ttsDir);
    voice.ok = !!(tts && tts.ok) && cached;
    voice.why = 'голос ' + (tts && tts.voice) + ', mp3 ' + (tts && tts.bytes) + ' байт, ' +
      'длительность ' + (tts && Math.round(tts.dur * 10) / 10) + ' с, кэш ' + cached;
  } catch (err) {
    console.error('[friend] selftest: озвучка не проверилась:', err.message);
    voice = { ok: false, why: err.message };
  }
  console.log('[friend] selftest: озвучка Edge TTS — ' +
    (voice.ok ? 'OK (' + voice.why + ')' : 'ОШИБКА (' + voice.why + ')'));

  // Тринадцатая часть: голосовой ввод (этап 7, локальный faster-whisper).
  // Гоняем полную петлю голоса: её собственная озвученная фраза (mp3 с /tts)
  // → страница пересобирает её в WAV 16 кГц (тот же путь, что у микрофонной
  // кнопки) → /stt → текст. Так проверяется вся цепочка распознавания без
  // живого микрофона. Плюс сервер обязан заявить stt в welcome и кнопка
  // микрофона обязана по этому флагу показаться.
  let stt = { ok: false, why: 'страница не ответила' };
  try {
    const voiceLoop = await win.webContents.executeJavaScript(`(async () => {
      const info = window.friendApp && window.friendApp.sttInfo && window.friendApp.sttInfo();
      if (!info || !info.available) return { ok: false, why: 'голосовой ввод не заявлен сервером' };
      const micOn = !!(window.friendChat && window.friendChat.micVisible());
      const phrase = 'Привет, Мира, как твои дела?';
      const res = await fetch('/tts?text=' + encodeURIComponent(phrase));
      if (!res.ok) return { ok: false, why: '/tts статус ' + res.status };
      const mp3 = await res.blob();
      const t0 = Date.now();
      const text = await window.friendApp.transcribe(mp3);
      return { ok: true, micOn: micOn, text: text, ms: Date.now() - t0 };
    })()`);
    const heard = ((voiceLoop && voiceLoop.text) || '').toLowerCase();
    stt.ok = !!(voiceLoop && voiceLoop.ok) && voiceLoop.micOn && heard.includes('привет');
    stt.why = 'услышано «' + ((voiceLoop && voiceLoop.text) || '') + '» за ' +
      (voiceLoop && voiceLoop.ms) + ' мс, кнопка микрофона ' + (voiceLoop && voiceLoop.micOn);
  } catch (err) {
    console.error('[friend] selftest: голосовой ввод не проверился:', err.message);
    stt = { ok: false, why: err.message };
  }
  console.log('[friend] selftest: голосовой ввод — ' +
    (stt.ok ? 'OK (' + stt.why + ')' : 'ОШИБКА (' + stt.why + ')'));

  // Четырнадцатая часть: wake word (этап 7, «слышит её имя»). Полная петля:
  // её озвученное «Мира, приветы!» (mp3 с /tts) → страница пересобирает в
  // WAV 16 кГц → POST /wake → сервер локально расшифровывает и узнаёт имя →
  // кадр wake → фронтенд будит её и отвечает на имя короткой фразой.
  // Фоновый слушатель при этом реально работает (getUserMedia + VAD):
  // проверяем и это, и что индикатор-слушание поднят.
  let wake = { ok: false, why: 'страница не ответила' };
  try {
    const res = await win.webContents.executeJavaScript(`(async () => {
      const fa = window.friendApp;
      const info = fa && fa.wakeState && fa.wakeState();
      if (!info || !info.available) return { ok: false, why: 'wake не заявлен сервером' };
      fa.wakeStart();
      let on = false;
      for (let i = 0; i < 60 && !on; i++) { await new Promise((r) => setTimeout(r, 100)); on = fa.wakeState().on; }
      const mp3res = await fetch('/tts?text=' + encodeURIComponent('Мира, приветы!'));
      if (!mp3res.ok) return { ok: false, why: '/tts статус ' + mp3res.status };
      const mp3 = await mp3res.blob();
      const sent = await fa.wakeSend(mp3);
      const heard = sent && sent.text || '';
      const waked = !!(sent && sent.wake);
      // Ищем облачко с фразой ответа на имя — она появляется сразу после
      // кадра wake (ответ на сами слова от модели приедет позже, он не важен).
      let phrase = '';
      const seen = [];
      for (let i = 0; i < 40 && !phrase; i++) {
        await new Promise((r) => setTimeout(r, 150));
        const b = document.querySelector('.bubble');
        if (b && !b.hidden) {
          const t = b.querySelector('.bubble-text');
          const s = t && t.textContent || '';
          if (s && !seen.includes(s)) seen.push(s);
          if (s && fa.wakePhrases().includes(s)) phrase = s;
        }
      }
      fa.wakeStop();
      return { ok: waked && !!phrase, listening: on, heard: heard, raw: JSON.stringify(sent && sent.text), events: fa.wakeEvents(), seen: seen.join(' / '), phrase: phrase };
    })()`);
    wake.ok = !!(res && res.ok);
    wake.why = 'услышано ' + (res && res.raw) + ', wake ' + wake.ok +
      ', слушатель ' + (res && res.listening) + ', событий wake ' + (res && res.events) +
      ', облачка: ' + (res && res.seen) + ', фраза «' + (res && res.phrase) + '»';
  } catch (err) {
    console.error('[friend] selftest: wake word не проверился:', err.message);
    wake = { ok: false, why: err.message };
  }
  console.log('[friend] selftest: wake word — ' +
    (wake.ok ? 'OK (' + wake.why + ')' : 'ОШИБКА (' + wake.why + ')'));

  // Пятнадцатая часть: трей и автозапуск. Три вещи, за которые цепляется человек:
  // иконка трея существует и она НЕ пустая; закрытие окна прячет Миру (окно
  // живое, невидимое), а показ из трея возвращает её; автозапуск — настоящая
  // запись в штатном ключе Windows: применили, прочитали, вернули как было.
  let trayPart = { ok: false, why: 'трей не поднят' };
  try {
    const icon = trayImage ? trayImage.getSize() : { width: 0, height: 0 };
    const iconOk = !!(tray && !tray.isDestroyed() && icon.width > 0 && icon.height > 0);
    // Закрытие окна = прятанье. Проверяем честно: окно после close() живое.
    win.close();
    let hidden = false;
    for (let i = 0; i < 40 && !hidden; i++) {
      await new Promise((r) => setTimeout(r, 100));
      hidden = !win.isVisible() && !win.isDestroyed();
    }
    // «показать / скрыть» из меню трея — возвращает её на экран.
    trayToggle();
    let shown = false;
    for (let i = 0; i < 40 && !shown; i++) {
      await new Promise((r) => setTimeout(r, 100));
      shown = win.isVisible();
    }
    // Автозапуск: трогаем реестр и ОБЯЗАНЫ вернуть как было — чужая машина
    // не должна остаться с тестовой записью в автозагрузке.
    const loginBefore = autostartRead();
    const applied = autostartApply(true);
    const loginOn = autostartRead();
    autostartApply(loginBefore);
    const loginBack = autostartRead() === loginBefore;
    trayPart.ok = iconOk && hidden && shown && applied && loginOn && loginBack;
    trayPart.why = 'иконка ' + icon.width + '×' + icon.height +
      ', close→скрыта ' + hidden + ', из трея видна ' + shown +
      ', openAtLogin ' + loginBefore + '→' + (applied && loginOn) + '→вернул ' + loginBack;
  } catch (err) {
    console.error('[friend] selftest: трей не проверился:', err.message);
    trayPart = { ok: false, why: err.message };
  }
  console.log('[friend] selftest: трей и автозапуск — ' +
    (trayPart.ok ? 'OK (' + trayPart.why + ')' : 'ОШИБКА (' + trayPart.why + ')'));

  // Шестнадцатая часть: посадка «на окне». Тестовое окно посадки даёт помощник
  // в режиме -selftest (живое окно человека в прогоне не сообщается); оболочка
  // подставляет в него границы мишени. Проверяем три вещи: посадку из меню
  // точно на кромку; удержание ≥1 с при протяжке — сажает, быстрый пролёт —
  // нет; и смена мишени (переезд за новым окном).
  let seatPart = { ok: false, why: 'не проверено' };
  {
    stopFocusWatch();
    const wa = screen.getPrimaryDisplay().workArea;
    const target = { x: wa.x + 320, y: Math.max(wa.y + 430, 500), w: 560, h: 480 };
    selftestTarget = target;
    ensureFocusWatch(true);
    // Живой помощник в это время не работает: прогон не должен двигать её за
    // НАСТОЯЩИМ активным окном человека.
    // Высота окна и минимум ног — живые числа (agree-часть guarantees окно
    // 375 при pix=5; спрайт 280, паддинг сверху 84).
    const SPRITE_H = 280;
    const WIN_H = 375;
    // Ждём, пока страница примет тестовую строку с мишенью.
    let seenTarget = false;
    for (let i = 0; i < 60 && !seenTarget; i++) {
      await new Promise((r) => setTimeout(r, 100));
      try {
        seenTarget = await win.webContents.executeJavaScript(
          '!!(window.friendApp && window.friendApp.focusWin() && window.friendApp.focusWin().process === "SelftestTarget.exe")');
      } catch (_err) { seenTarget = false; }
    }
    // А) Посадка из меню: ноги — ровно на кромку мишени (не ниже минимума,
    //    выше которого окно физически не влезает), по горизонтали — внутри окна.
    moveWindow(300, 300);
    await new Promise((r) => setTimeout(r, 250));
    let menuSeat = null;
    try {
      menuSeat = await win.webContents.executeJavaScript(`(async () => {
        const fa = window.friendApp;
        fa.menu.setSeatQuiet('window');
        await fa.applySeat('window');
        // Гаркаем очередь drag.js (она одна на все переезды окна) и сверяем
        // место страницы с окном: если счётчики разъехались — чиним и садимся.
        await fa.drag.enqueue(() => fa.bridge.getPosition()).then((pos) => {
          if (pos && fa.winPos().x !== pos.x) fa.anchorFromWindow(pos);
        });
        const g0 = await fa.bridge.getPosition();
        if (Math.abs(g0.y - 300) > 40) { await fa.applySeat('window'); }
        const t = fa.focusWin();
        const a = fa.anchor();
        const w = await fa.bridge.getPosition();
        return {
          seat: fa.menu.settings().seat, ax: Math.round(a.x), ay: Math.round(a.y),
          // Ноги = низ окна минус нижний паддинг (окно на 10 px ниже ног).
          winTop: w.y, feet: w.y + fa.windowSize().h - fa.pads.bottom,
          // Окно шире фигуры: проверяем НОГИ по горизонтали, а не края окна.
          feetX: w.x + fa.pads.x + Math.round(fa.avatar.width / 2), t: t,
        };
      })()`);
    } catch (_err) { /* ниже разберёмся по null */ }
    // Ожидаемые НОГИ посадки: кромка мишени, но не ниже низа рабочей области
    // и не выше минимума, при котором окно (375 px) физически влезает на экран
    // — ровно те же границы, что применяет страница.
    const minFeet = wa.y + 84 + SPRITE_H;
    const expY = Math.min(Math.max(target.y, minFeet), wa.y + wa.height);
    const menuOk = !!(menuSeat && menuSeat.seat === 'window' && menuSeat.t &&
      Math.abs(menuSeat.feet - expY) <= 3 &&
      menuSeat.winTop >= wa.y - 1 &&
      menuSeat.feetX >= target.x && menuSeat.feetX <= target.x + target.w);

    // Б) Удержание: тащим её к кромке и «стоим» в полосе >1 с — обязана сесть.
    let dwell = null;
    try {
      dwell = await win.webContents.executeJavaScript(`(async () => {
        const fa = window.friendApp;
        fa.menu.setSeat('free');
        await fa.applySeat('free');
        await new Promise((r) => setTimeout(r, 250));
        const g = await fa.bridge.getPosition();
        const t = fa.focusWin();
        const rect = fa.app.view.getBoundingClientRect();
        const cx = rect.left + fa.avatar.container.x;
        const cy = rect.top + fa.avatar.container.y;
        const mk = (type, sx, sy, btns) => new PointerEvent(type, {
          bubbles: true, cancelable: true, pointerId: 11, isPrimary: true,
          buttons: btns, clientX: cx, clientY: cy, screenX: sx, screenY: sy,
        });
        const stage = document.getElementById('stage');
        stage.dispatchEvent(mk('pointerdown', g.x + cx, g.y + cy, 1));
        // Верх окна при ногах на кромке+6: окно на (высота − нижний паддинг) ниже ног.
        const winYwant = t.y + 6 - (fa.windowSize().h - fa.pads.bottom);
        const dy = winYwant - g.y;
        const steps = 16;
        for (let i = 1; i <= steps; i++) {
          window.dispatchEvent(mk('pointermove', g.x + cx, g.y + cy + Math.round(dy * i / steps), 1));
          await new Promise((r) => setTimeout(r, 80));
        }
        // «Стоим» на кромке: мелкие шевеления внутри полосы, всего ~1.1 с.
        for (let i = 0; i < 11; i++) {
          window.dispatchEvent(mk('pointermove', g.x + cx, g.y + cy + Math.round(dy + (i % 2)), 1));
          await new Promise((r) => setTimeout(r, 100));
        }
        const seated = fa.menu.settings().seat === 'window';
        const a1 = fa.anchor();
        window.dispatchEvent(mk('pointerup', g.x + cx, g.y + cy + dy + 5, 0));
        await new Promise((r) => setTimeout(r, 400));
        const a2 = fa.anchor();
        return {
          seated,
          feetDuring: Math.abs(a1.y - t.y),
          feetAfter: Math.abs(a2.y - t.y),
          seatAfter: fa.menu.settings().seat,
        };
      })()`);
    } catch (err) {
      console.error('[friend] selftest: удержание не проверилось:', err.message);
    }
    // После усаживания протяжка ещё «доезжает» (settle), ноги могут остаться
    // в паре пикселей над кромкой — это норма; больше 8 px — соскочила.
    const dwellOk = !!(dwell && dwell.seated && dwell.seatAfter === 'window' &&
      dwell.feetAfter <= 8);

    // В) Быстрый пролёт через кромку — посадить не должен.
    let fast = null;
    try {
      fast = await win.webContents.executeJavaScript(`(async () => {
        const fa = window.friendApp;
        fa.menu.setSeat('free');
        await fa.applySeat('free');
        await new Promise((r) => setTimeout(r, 250));
        const g = await fa.bridge.getPosition();
        const t = fa.focusWin();
        const rect = fa.app.view.getBoundingClientRect();
        const cx = rect.left + fa.avatar.container.x;
        const cy = rect.top + fa.avatar.container.y;
        const mk = (type, sx, sy, btns) => new PointerEvent(type, {
          bubbles: true, cancelable: true, pointerId: 12, isPrimary: true,
          buttons: btns, clientX: cx, clientY: cy, screenX: sx, screenY: sy,
        });
        const stage = document.getElementById('stage');
        stage.dispatchEvent(mk('pointerdown', g.x + cx, g.y + cy, 1));
        const winYwant = t.y - 40 - (fa.windowSize().h - fa.pads.bottom); // пролёт выше кромки
        const dy = winYwant - g.y;
        for (let i = 1; i <= 12; i++) {
          window.dispatchEvent(mk('pointermove', g.x + cx, g.y + cy + Math.round(dy * i / 12), 1));
          await new Promise((r) => setTimeout(r, 12));
        }
        window.dispatchEvent(mk('pointerup', g.x + cx, g.y + cy + dy, 0));
        await new Promise((r) => setTimeout(r, 1500));
        return { seat: fa.menu.settings().seat };
      })()`);
    } catch (err) {
      console.error('[friend] selftest: быстрый пролёт не проверился:', err.message);
    }
    const fastOk = !!(fast && fast.seat === 'free');

    // Г) Смена мишени: сидит на окне — мишень уехала — она переезжает следом.
    let follow = null;
    try {
      const target2 = { x: wa.x + 220, y: wa.y + 560, w: 640, h: 380 };
      selftestTarget = target2;
      follow = await win.webContents.executeJavaScript(`(async () => {
        const fa = window.friendApp;
        fa.menu.setSeat('window');
        await fa.applySeat('window');
        const T = ${JSON.stringify(target2)};
        let targetNow = null;
        for (let i = 0; i < 80 && !targetNow; i++) {
          await new Promise((r) => setTimeout(r, 100));
          const fw = fa.focusWin();
          if (fw && fw.y === T.y) targetNow = fw;
        }
        if (!targetNow) return { ok: false, why: 'мишень не приехала' };
        let a2 = fa.anchor();
        for (let i = 0; i < 40 && Math.abs(a2.y - T.y) > 3; i++) {
          await new Promise((r) => setTimeout(r, 100));
          a2 = fa.anchor();
        }
        return { ok: Math.abs(a2.y - T.y) <= 3 && a2.x >= T.x && a2.x <= T.x + T.w, ay: Math.round(a2.y), ty: T.y, ax: Math.round(a2.x) };
      })()`);
    } catch (err) {
      console.error('[friend] selftest: смена мишени не проверилась:', err.message);
    }
    const followOk = !!(follow && follow.ok);

    seatPart = {
      ok: seenTarget && menuOk && dwellOk && fastOk && followOk,
      why: 'мишень ' + seenTarget +
        ', из меню на ' + (menuSeat ? menuSeat.feet + '/' + expY : '?') +
        ', удержание ' + (dwell ? dwell.seated + ' ноги ' + dwell.feetAfter : '?') +
        ', пролёт ' + (fast ? fast.seat : '?') +
        ', смена ' + (follow ? (follow.ok ? follow.ay + '/' + follow.ty : 'ОШ (' + follow.why + ')') : '?'),
    };
    // Возвращаем живого помощника и свободную посадку: тест не оставляет
    // после себя тестовое окно в контексте и персонажа на чужом окне.
    selftestTarget = null;
    stopFocusWatch();
    ensureFocusWatch();
    await win.webContents.executeJavaScript(`(async () => {
      window.friendApp.menu.setSeat('free');
      await window.friendApp.applySeat('free');
    })()`).catch(() => {});
    await new Promise((r) => setTimeout(r, 200));
  }
  console.log('[friend] selftest: посадка на окне — ' +
    (seatPart.ok ? 'OK (' + seatPart.why + ')' : 'ОШИБКА (' + seatPart.why + ')'));

  // Двадцать первая часть: сила слежения за курсором (ползунок 0..100).
  // 0 — зрачки по центру и курсор их не сдвигает; 100 — доезжают до предела
  // (величина PIX, в аватаре это один пиксель спрайта — «вылупившихся глаз»
  // не бывает); 50 — ровно половина предела. Ползунком мышью не двигаем:
  // input-элемент меню живёт в DOM всегда (панель просто скрыта), и событие
  // 'input' на нём делает ровно то же, что движение ползунка.
  let gaze = null;
  try {
    gaze = await win.webContents.executeJavaScript(`(async () => {
      const fa = window.friendApp;
      const rect = fa.app.view.getBoundingClientRect();
      const input = document.querySelector('input[aria-label="сила слежения за курсором"]');
      if (!input) return { ok: false, why: 'ползунка в меню нет' };
      const measure = async (p) => {
        input.value = String(p);
        input.dispatchEvent(new Event('input', { bubbles: true }));
        // Курсор — далеко за верхним правым углом окна: вектор за пределами
        // «половины ширины», величина взглята упёрта в предел, и сила ползунка
        // видна на ней напрямую.
        window.dispatchEvent(new PointerEvent('pointermove', {
          clientX: rect.right + 600, clientY: rect.top - 400, bubbles: true,
        }));
        await new Promise((r) => setTimeout(r, 300));
        const s = fa.avatar.lookState();
        return Math.hypot(s.x, s.y);
      };
      const m0 = await measure(0);
      const m50 = await measure(50);
      const m100 = await measure(100);
      // Ползунок возвращаем на значение человека (файл настроек восстановят
      // ниже; live-состояние страницы восстанавливаем здесь).
      const back = fa.menu.settings().follow;
      input.value = String(back);
      input.dispatchEvent(new Event('input', { bubbles: true }));
      return {
        ok: m0 < 0.05 && m100 >= 4.5 && Math.abs(m50 - m100 / 2) <= 0.3,
        m0: Math.round(m0 * 100) / 100, m50: Math.round(m50 * 100) / 100,
        m100: Math.round(m100 * 100) / 100,
      };
    })()`);
  } catch (err) {
    console.error('[friend] selftest: сила слежения не проверилась:', err.message);
  }
  gaze = gaze || { ok: false };
  console.log('[friend] selftest: сила слежения — ' +
    (gaze.ok ? 'OK' : 'ОШИБКА') + ' (0 → ' + gaze.m0 +
    ', 50 → ' + gaze.m50 + ', 100 → ' + gaze.m100 + ')');

  // Двадцать вторая часть: «реакция на звук» — танец под музыку. Детектор
  // гоняется тестовыми уровнями (feed-режим js/music.js): настоящего звука
  // в проверке нет, логика та же. Заодно смотрим, что захват открывается
  // graceful: на машине без звукового устройства модуль сообщает «источника
  // нет» и танца просто не будет — без падения.
  let musicPart = null;
  try {
    musicPart = await win.webContents.executeJavaScript(`(async () => {
      const fa = window.friendApp;
      const m = fa.music;
      if (!m) return { ok: false, why: 'модуля звука нет' };
      const real = await m.start();
      const realState = m.state();
      m.stop();
      m.feedStart();
      const t = (ms) => new Promise((r) => setTimeout(r, ms));
      // Тишина: пусть порог комнаты опустится до реального.
      for (let i = 0; i < 25; i++) { m.feedLevelSet(0.012); await t(100); }
      const floorQuiet = m.state().floor;
      // «Музыка» ~150 уд/мин: всплеск каждые 4 тика, между ними — фон.
      let dancing = false;
      let bpm = 0;
      for (let k = 0; k < 55 && !dancing; k++) {
        m.feedLevelSet(k % 4 === 0 ? 0.26 : 0.05);
        await t(100);
        const s = m.state();
        dancing = s.dancing;
        bpm = s.bpm;
      }
      const avatarDancing = fa.avatar.dancing();
      // Тишина 8 с — танец должен закончиться, аватар перестать подпрыгивать.
      let off = false;
      for (let k = 0; k < 115 && !off; k++) {
        m.feedLevelSet(0.012);
        await t(100);
        off = m.state().dancing === false;
      }
      const avatarOff = !fa.avatar.dancing();
      m.feedStop();
      return {
        ok: floorQuiet < 0.02 && dancing && bpm >= 90 && bpm <= 170 &&
          avatarDancing && off && avatarOff,
        why: 'пол ' + floorQuiet + ', танец ' + dancing + ' ' + bpm +
          ' у/мин аватар ' + avatarDancing + ', тишина off ' + off +
          ' аватар ' + avatarOff + ', помощник ' + real +
          (realState.error ? ' (' + realState.error + ')' : ''),
      };
    })()`);
  } catch (err) {
    console.error('[friend] selftest: танец под музыку не проверился:', err.message);
  }
  musicPart = musicPart || { ok: false, why: 'нет ответа' };
  console.log('[friend] selftest: танец под музыку — ' +
    (musicPart.ok ? 'OK (' + musicPart.why + ')' : 'ОШИБКА (' + musicPart.why + ')'));

  // Двадцать третья часть: «сон/уход после простоя» (js/idlewatch.js). Простой
  // задаём числами (feed) без ожидания получаса: 12 минут тишины — сон,
  // «вернулся» — сон снят; 35 минут — уход к краю (окно у кромки экрана);
  // «вернулся» — бодрствует и окно на прежнем месте.
  let idlePart = null;
  {
    const waS = screen.getPrimaryDisplay().workArea;
    const b0 = win.getBounds();
    idlePart = await win.webContents.executeJavaScript(`(async () => {
      const fa = window.friendApp;
      const idle = fa.idle;
      if (!idle) return { ok: false, why: 'модуля idlewatch нет' };
      const t = (ms) => new Promise((r) => setTimeout(r, ms));
      const wait = async (fn, limit) => {
        const t0 = Date.now();
        while (Date.now() - t0 < limit) { if (fn()) return true; await t(200); }
        return false;
      };
      idle.feed(12 * 60 * 1000);
      const slept = await wait(() => fa.avatar.emotion() === 'sleep' &&
        idle.state().phase === 'asleep', 12000);
      idle.feed(1000);
      const woke = await wait(() => idle.state().phase === 'awake' &&
        fa.avatar.emotion() !== 'sleep', 12000);
      idle.feed(35 * 60 * 1000);
      const gone = await wait(() => idle.state().phase === 'gone' &&
        fa.avatar.emotion() === 'sleep', 12000);
      // Кромку фиксируем, пока она ещё у края: следом окно поедет обратно.
      // Ждём, пока позиция перестанет меняться (переезд к краю — цепочка
      // IPC, на загрузке она растягивается).
      let edgeAt = null;
      let prevX = -1;
      let stable = 0;
      for (let i = 0; i < 40; i++) {
        const x = await fa.bridge.getPosition().then((p) => p && p.x);
        if (x === prevX) { stable++; if (stable >= 3) { edgeAt = x; break; } }
        else stable = 0;
        prevX = x;
        await t(120);
      }
      if (edgeAt === null) edgeAt = prevX;
      idle.feed(1000);
      const back = await wait(() => idle.state().phase === 'awake' &&
        fa.avatar.emotion() !== 'sleep', 12000);
      idle.feed(null);
      return { slept: slept, woke: woke, gone: gone, back: back, edgeAt: edgeAt };
    })()`).catch((err) => ({
      ok: false, why: 'окно страницы: ' + err.message,
      slept: false, woke: false, gone: false, back: false,
    }));
    // Даём окну доехать до края (edgeAt зафиксирован в момент «ушла»).
    await new Promise((r) => setTimeout(r, 400));
    // Окно у края и обратно меряем со стороны настоящей геометрии.
    const bMid = win.getBounds();
    idlePart.atEdge = typeof idlePart.edgeAt === 'number' &&
      (Math.abs(idlePart.edgeAt - waS.x) <= 3 ||
        Math.abs(idlePart.edgeAt + bMid.width - (waS.x + waS.width)) <= 3);
    const bEnd = win.getBounds();
    idlePart.movedBack = Math.abs(bEnd.x - b0.x) <= 4;
    idlePart.ok = idlePart.slept && idlePart.woke && idlePart.gone &&
      idlePart.atEdge && idlePart.back && idlePart.movedBack;
    console.log('[friend] selftest: сон/уход после простоя — ' +
      (idlePart.ok ? 'OK (сон ' + idlePart.slept + ', пробуждение ' + idlePart.woke +
        ', уход ' + idlePart.gone + ' у края ' + idlePart.atEdge +
        ', возврат ' + idlePart.back + ' на место ' + idlePart.movedBack + ')'
        : 'ОШИБКА (' + JSON.stringify(idlePart) + ')'));
  }

  // Двадцать четвёртая часть: «ходьба по экрану» — длинный проход. Стартует
  // маршрут (к дальнему краю, разворот, полный переход к другому), страница
  // сама следит за направлениями и положением. scale=8 — ускорение только
  // для проверки: настоящий маршрут шёл бы минуту.
  let routePart = null;
  {
    const waR = screen.getPrimaryDisplay().workArea;
    routePart = await win.webContents.executeJavaScript(`(async () => {
      const fa = window.friendApp;
      if (!fa.walkRoute) return { ok: false, why: 'маршрута нет' };
      // Пиксельная проверка цикла шага: в ОБОИХ кадрах ноги — две группы
      // колонок с зазором (не слились в центр), кадры зеркальны друг другу.
      const legs = fa.avatar.walkLegTest();
      const legOk = legs.length === 2 && legs.every((g) =>
        g.length === 2 && g[1][0] - g[0][1] >= 5);
      // Зеркальность кадров: flipX — это x' = GW-1-x (GW=40). Первый кадр —
      // передняя нога справа, второй — её зеркало.
      const legMirrored = legs.length === 2 && legs[0].length === 2 &&
        legs[1].length === 2 &&
        legs[1][0][0] + legs[0][1][1] === 39 &&
        legs[1][0][1] + legs[0][1][0] === 39;
      fa.stopWalk('перед маршрутом');
      fa.walkRoute(8);
      const t = (ms) => new Promise((r) => setTimeout(r, ms));
      let minx = 1e9, maxx = -1e9, sawTurn = false, lastDir = 0, ended = false;
      const t0 = Date.now();
      while (Date.now() - t0 < 26000 && !ended) {
        const p = await fa.bridge.getPosition();
        if (p && typeof p.x === 'number') { minx = Math.min(minx, p.x); maxx = Math.max(maxx, p.x); }
        const d = fa.avatar.walking();
        if (d !== 0 && lastDir !== 0 && d !== lastDir) sawTurn = true;
        if (d !== 0) lastDir = d;
        if (d === 0 && Date.now() - t0 > 1500) ended = true;
        if (!ended) await t(250);
      }
      const walking = fa.avatar.walking();
      fa.stopWalk('самопроверка кончила маршрут');
      const pf = await fa.bridge.getPosition();
      const wa = await fa.bridge.workArea();
      const full = wa.width - 248;
      const range = maxx - minx;
      return {
        ok: ended && sawTurn && range >= full * 0.5 && legOk && legMirrored &&
          pf.x >= wa.x - 2 && pf.x + 248 <= wa.x + wa.width + 2,
        why: 'размах ' + range + ' из ' + full + ', разворот ' + sawTurn +
          ', ноги ' + JSON.stringify(legs) + ' зеркало ' + legMirrored +
          ', конец ' + ended + ', финал ' + pf.x,
      };
    })()`).catch((err) => ({ ok: false, why: 'окно страницы: ' + err.message }));
    console.log('[friend] selftest: ходьба по экрану — ' +
      (routePart.ok ? 'OK (' + routePart.why + ')' : 'ОШИБКА (' + routePart.why + ')'));
  }

  // Настройки возвращаем те, что были ДО проверки: она трогает ползунок размера
  // и посадку, а это выбор человека, а не тестовые данные. Иначе после каждого
  // прогона её размер и посадка сбрасывались бы к тестовым значениям.
  console.log('[friend] selftest: маяк — возврат настроек');
  writeSettings(keepSettings);
  await win.webContents.executeJavaScript(
    'try { localStorage.setItem("friend.settings.v1", ' + JSON.stringify(JSON.stringify(keepSettings)) + '); } catch (_e) {}',
  ).catch(() => {});
  console.log('[friend] selftest: маяк — localStorage вернулся');

  // Синтетические проверки кончились — возвращаем окну живую мышь.
  quietMouse(false);

  // Заканчиваем в домашнем углу: после проверок окно должно остаться там, где
  // человек его ожидает, а сервер — знать это место.
  const home = moveWindow(screen.getPrimaryDisplay().workArea.width - 360, screen.getPrimaryDisplay().workArea.height - 500);
  console.log('[friend] selftest: маяк — окно дома ' + JSON.stringify(home));
  await win.webContents.executeJavaScript(`window.friendApp.net.sendPosition(${home.x}, ${home.y})`).catch(() => {});
  console.log('[friend] selftest: маяк — позиция отправлена');
  await new Promise((r) => setTimeout(r, 300));

  console.log('[friend] selftest: маяк — выход');
  app.exit(agree.ok && dragged && gated && sizeHeld && dragAccurate && chat.ok && pet.ok &&
    sleep.ok && mood.ok && walk.ok && poke.ok && menuTest.ok && windowVisible && savedVisible && focusChain.ok && vanish.ok && sheet.ok && voice.ok && stt.ok && wake.ok && trayPart.ok && seatPart.ok && gaze.ok && musicPart.ok && idlePart.ok && routePart.ok ? 0 : 1);
}

// --- Жизненный цикл -------------------------------------------------------

// Одновременно должен жить РОВНО ОДИН компаньон: два окна — это два персонажа
// на экране и два подключения к одному серверу, которые спорят за сохранённую
// позицию (что и случилось при двух запусках подряд). Исключение — самопроверка:
// ей нужен свой экземпляр, пока рабочий компаньон продолжает жить.
if (!SELFTEST && !app.requestSingleInstanceLock()) {
  console.log('[friend] компаньон уже запущен: второе окно не открываю');
  app.quit();
} else {
  app.on('second-instance', () => {
    if (win) {
      win.show();
      win.focus();
    }
  });
}

app.on('window-all-closed', () => app.quit());
app.on('before-quit', () => {
  // Выход — настоящий: окно перестаёт прятаться в трей и закрывается по-настоящему,
  // иначе «выход» из трея уткнулся бы в собственный запрет закрытия.
  isQuitting = true;
  if (tray) { tray.destroy(); tray = null; }
  stopFocusWatch();
  stopSoundWatch();
  // Сервер поднимали мы — мы же и гасим, иначе после закрытия окна останется
  // висеть процесс на 8787 и следующий запуск подцепится к нему.
  if (serverProc && !serverProc.killed) {
    serverProc.kill();
    serverProc = null;
  }
});

app.whenReady().then(async () => {
  const ok = await ensureServer();
  if (!ok) console.error('[friend] продолжаем без сервера: окно покажет подсказку');
  createWindow();
  createTray();
  ensureFocusWatch();

  // Автозапуск: пожелание человека — в настройках, исполнение — в ключе
  // автозапуска Windows. Ключ может быть удален руками (Task Manager, «Автозагрузка»),
  // тогда по настройке «включено» восстанавливаем его при старте. Если человек
  // выключил сам — не навязываем.
  if (readSettings().autostart && !autostartRead()) {
    autostartApply(true);
    console.log('[friend] автозапуск: настройка включена, ключ восстановлен');
  }

  // Сторож: проверка раз в три секунды и на любое изменение экранов. Изменение
  // конфигурации (отключили монитор, поменяли масштаб) — самая частая причина
  // «персонаж исчез».
  setInterval(() => {
    ensureOnScreen('проверка');
    void checkDrawing('проверка');
  }, 3000);
  // Пробуждение системы и разблокировка экрана — время, в которое теряются
  // контексты отрисовки и настройки «поверх всех». Проверяем и то и другое
  // сразу: здесь важно вернуть персонажа, а не выяснить, что именно отвалилось.
  powerMonitor.on('resume', () => { ensureOnScreen('система проснулась'); void checkDrawing('система проснулась'); });
  powerMonitor.on('unlock-screen', () => { ensureOnScreen('экран разблокирован'); void checkDrawing('экран разблокирован'); });
  // Графический процесс — отдельный процесс Chromium: когда он падает, окно и
  // страница живы, но рисовать им больше нечем.
  app.on('child-process-gone', (_event, details) => {
    const type = details ? details.type : '?';
    const reason = details ? details.reason : '?';
    console.error('[friend] процесс упал: ' + type + ' (' + reason + ')');
    if (win && type === 'GPU') win.reload();
  });

  screen.on('display-added', () => { ensureOnScreen('подключили экран'); void checkDrawing('подключили экран'); });
  screen.on('display-removed', () => { ensureOnScreen('отключили экран'); void checkDrawing('отключили экран'); });
  screen.on('display-metrics-changed', () => { ensureOnScreen('изменились настройки экрана'); void checkDrawing('изменились настройки экрана'); });

  if (SELFTEST) {
    // Ждём, пока страница действительно соберётся: самопроверка меряет
    // сдвиг окна, а не скорость загрузки.
    win.webContents.once('did-finish-load', () => {
      setTimeout(() => {
        runSelfTest().catch((err) => {
          console.error('[friend] selftest упал:', err);
          app.exit(1);
        });
      }, 1200);
    });
  }
});
