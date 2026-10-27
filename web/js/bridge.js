// bridge.js — мост к оболочке (Electron / Tauri) с dev-режимом для браузера.
//
// Зачем отдельный слой: персонаж и вся логика — обычная веб-страница, а
// «перетащить окно», «всегда сверху» и «выйти» умеет только оболочка. Мост
// прячет эту разницу за четырьмя функциями, поэтому:
//   • в браузере персонажа можно проверять и отлаживать (двигаем аватар внутри
//     страницы, протокол и код перетаскивания — те же самые);
//   • переезд Electron → Tauri (или наоборот) не трогает js/avatar.js,
//     js/net.js, js/main.js — меняется только этот файл и обёртка.
//
// Позиция всюду трактуется одинаково: ЛЕВЫЙ ВЕРХНИЙ угол окна на экране, в
// пикселях. В браузере «экран» — вьюпорт, а угол окна — смещение аватара.
window.FriendBridge = (function () {
  'use strict';

  const api = {
    mode: 'browser',
    isNative: false,
    // Реальный размер окна: его задаёт конфиг сервера (avatar.width/height).
    size: { width: 320, height: 420 },
    // Настройки персонажа от оболочки (файл рядом с состоянием) и запись. По
    // умолчанию их нет — тогда страница берёт своё из localStorage: так живёт
    // браузерный dev-режим, где оболочки нет вовсе.
    settings: null,
    saveSettings: () => {},
    getShellSettings: async () => null,
    // Автозапуск: в браузере запустить себя при входе в систему нечем.
    setAutostart: async () => false,
    // В браузере источника системного микшера нет и быть не может — танец
    // просто не заводится (js/music.js). setDanceCapture просит оболочку
    // поднять/погасить помощника soundwatch; onSound — подписка на RMS-число.
    setDanceCapture: async () => false,
    onSound: () => {},
    // Подписка страницы на «окно поставили не туда, куда просили» (см. ветку
    // Electron ниже): страница отправляет настоящее место серверу.
    onPositionCorrected: null,
    // Подписка на смену активного окна человека (есть только в Electron,
    // см. ветку Electron ниже): по умолчанию — ничего не подписывать.
    onFocus: () => {},
  };

  // serverBase — откуда доступен сервер: пусто, когда страница им же и
  // отдана (Electron, браузер); http://127.0.0.1:8787, когда оболочка
  // (Tauri) отдаёт страницу из своего ассет-протокола (адрес вписывает
  // initialization script — window.__FRIEND_SERVER__). Пустая строка — это
  // «тот же origin»: net.js по ней понимает, что адрес собирать не надо.
  api.serverBase = window.__FRIEND_SERVER__ || '';
  window.FRIEND_SERVER_BASE = api.serverBase;
  window.FriendServerURL = function (path) { return api.serverBase + path; };

  // Точка отсчёта dev-режима: то место, куда оболочка поставила бы окно на
  // первом запуске (правый нижний угол). Всё, что дальше, — настоящее смещение
  // в экранных пикселях, поэтому протокол и арифметика перетаскивания в
  // браузере и в оболочке одни и те же: сдвинул на 100 вправо — позиция
  // увеличилась на 100.
  const dev = { x: 0, y: 0, home: { x: 0, y: 0 }, moved: 0 };
  let onDevMove = null;   // колбэк для main.js: подвинуть аватар в странице

  function computeHome() {
    dev.home = homePoint();
  }

  // homePoint — «домашняя» позиция окна: правый нижний угол рабочей области.
  // Считается одинаково для всех режимов (в браузере screen — это параметры
  // монитора, а не вкладки), поэтому «первый запуск» ведёт себя предсказуемо.
  function homePoint() {
    const sw = window.screen ? window.screen.availWidth : window.innerWidth;
    const sh = window.screen ? window.screen.availHeight : window.innerHeight;
    return {
      x: Math.max(0, sw - api.size.width - 40),
      y: Math.max(0, sh - api.size.height - 80),
    };
  }

  // ensureInit — первый вызов moveBy/setPosition до setSize: считаем размеры
  // сами, иначе дельта прибавилась бы к нулю и окно улетело в угол 0,0.
  function ensureInit() {
    if (!dev.inited) {
      dev.inited = true;
      computeHome();
      dev.x = dev.home.x;
      dev.y = dev.home.y;
    }
  }

  // notify отдаёт в страницу СМЕЩЕНИЕ от домашней точки: по нему видно, куда
  // персонаж «уехал», не зная размера экрана.
  function notify() {
    if (onDevMove) onDevMove(dev.x - dev.home.x, dev.y - dev.home.y);
  }

  // --- Electron: preload отдаёт window.friendNative (ipcRenderer.invoke) ---
  if (window.friendNative) {
    api.mode = 'electron';
    api.isNative = true;
    api.home = homePoint;
    api.setSize = async (w, h) => window.friendNative.setSize(w, h);
    api.setAlwaysOnTop = async (v) => await window.friendNative.setAlwaysOnTop(v);
    // focus нужен разговору: окно висит поверх всех и часто не в фокусе (человек
    // работает в программе под ним), а набранный текст пошёл бы туда.
    api.focus = async () => await window.friendNative.focus();
    api.quit = async () => await window.friendNative.quit();
    // Автозапуск при входе в систему — только дело оболочки (ключ автозапуска
    // Windows). Возвращает «применилось ли», меню по этому числу рисует чекбокс.
    api.setAutostart = async (on) => !!(await window.friendNative.autostart(!!on));
    // «Танцует под музыку» (js/music.js): setDanceCapture поднимает/гасит
    // помощника soundwatch (WASAPI-лупбек микшера), onSound — RMS-числа от
    // него. Из звука наружу живёт ровно одно число, в локальную страницу.
    api.setDanceCapture = async (on) => !!(await window.friendNative.dance(!!on));
    api.onSound = (cb) => { if (window.friendNative.onSound) window.friendNative.onSound(cb); };
    // workArea — рабочая область экрана, ТО ЕСТЬ БЕЗ ПАНЕЛИ ЗАДАЧ. Нужна
    // посадке: её верхняя кромка и есть место, куда персонаж садится.
    api.workArea = async () => await window.friendNative.workArea();
    // Пульс кадров: у оболочки есть сторож, который возвращает персонажа, если
    // отрисовка замолчала (см. desktop/main.js, BEAT_TIMEOUT). В браузере
    // сторожа нет, поэтому пульс там — пустая функция.
    api.beat = () => { if (window.friendNative.beat) window.friendNative.beat(); };
    // Понимание окна: оболочка передаёт события от помощника focuswatch
    // (активное приложение человека). Подписка, а не вызов: события приходят
    // сами, когда человек переключает окна.
    api.onFocus = (fn) => { if (window.friendNative.onFocus) window.friendNative.onFocus(fn); };
    // Настройки персонажа (размер, посадка, переключатели). Оболочка читает их из
    // файла и отдаёт странице при загрузке; запись — по каждому изменению.
    // localStorage для этого не годится: start.bat гасит прошлое окно силой, и
    // браузерное хранилище не успевает сброситься на диск (см. desktop/main.js).
    api.settings = window.friendNative.settings || null;
    // Свежие настройки из файла: снимок выше устаревает после перезагрузки
    // страницы — она перечитывает файл (invoke).
    api.getShellSettings = async () => {
      const s = await window.friendNative.getSettings();
      return s && typeof s === 'object' ? s : null;
    };
    api.saveSettings = (settings) => {
      if (window.friendNative.saveSettings) window.friendNative.saveSettings(settings);
    };

    // Место окна может НЕ совпасть с тем, куда его просили: оболочка держит
    // окно в видимой части рабочего стола и не пускает его за край. Тогда
    // настоящее место надо сообщить серверу (api.onPositionCorrected): если
    // оставить в памяти несуществующий адрес, окно будет возвращаться в никуда
    // при каждом запуске, и человек останется без персонажа.
    let known = null;
    function report(asked, real) {
      if (!real) return real;
      if (asked && (Math.abs(asked.x - real.x) > 1 || Math.abs(asked.y - real.y) > 1)) {
        if (api.onPositionCorrected) api.onPositionCorrected(real);
      }
      known = { x: real.x, y: real.y };
      return real;
    }

    api.getPosition = async () => {
      const p = await window.friendNative.getPosition();
      known = { x: p.x, y: p.y };
      return p;
    };
    api.setPosition = async (x, y) => report(
      { x: Math.round(x), y: Math.round(y) },
      await window.friendNative.setPosition(x, y),
    );
    api.moveBy = async (dx, dy) => {
      const asked = known ? { x: Math.round(known.x + dx), y: Math.round(known.y + dy) } : null;
      return report(asked, await window.friendNative.moveBy(dx, dy));
    };
    api.setBounds = async (b) => {
      // Размер запоминаем у себя: по api.size считается «домашняя» точка
      // (правый нижний угол экрана). Пока размер здесь не обновлялся, дом считался
      // по старому числу, и в углу она свешивалась за край.
      if (b && b.width && b.height) api.size = { width: Math.round(b.width), height: Math.round(b.height) };
      return report(
        b && typeof b.x === 'number' ? { x: Math.round(b.x), y: Math.round(b.y) } : null,
        await window.friendNative.setBounds(b),
      );
    };
    return api;
  }

  // --- Tauri: window.__TAURI__ появляется из @tauri-apps/api (withGlobalTauri) ---
  //
  // Полный паритет с веткой Electron (этап 15): те же команды и события, что
  // в desktop-tauri/src-tauri/src/main.rs. Настройки оболочка вписывает в
  // страницу до её скриптов (window.__FRIEND_SETTINGS__, initialization
  // script) — их читать синхронно при сборке меню, как у Электрона.
  if (window.__TAURI__) {
    api.mode = 'tauri';
    api.isNative = true;
    const t = window.__TAURI__;
    const inv = (cmd, args) => t.core.invoke(cmd, args);
    const listen = (event, fn) => t.event.listen(event, fn);

    api.home = homePoint;
    api.setSize = async (w, h) => { await inv('set_size', { width: Math.round(w), height: Math.round(h) }); };
    api.setAlwaysOnTop = async (v) => { await inv('always_on_top', { on: !!v }); };
    // focus нужен разговору: окно висит поверх всех и часто не в фокусе.
    api.focus = async () => { await inv('focus'); };
    api.quit = async () => { await inv('quit'); };
    api.setAutostart = async (on) => !!(await inv('autostart', { on: !!on }));
    api.setDanceCapture = async (on) => !!(await inv('dance', { on: !!on }));
    api.onSound = (cb) => { listen('sound:level', (e) => cb(e.payload)); };
    api.onFocus = (fn) => {
      listen('focus:info', (e) => fn(e.payload));
      // Страница переподключилась: просим последнюю строку помощника заново
      // (у Электрона оболочка сама повторяет lastFocusData при загрузке).
      inv('replay_focus');
    };
    api.workArea = async () => await inv('work_area');
    // Живая иконка трея: страница сэмплирует свой канвас (capturePage у
    // Tauri нет) и присылает 16×16 RGBA. Ветке Electron не нужна — там
    // оболочка снимает окно сама.
    api.setTrayIcon = (rgba, w, h) => inv('set_tray_icon', { rgba: rgba, width: w, height: h });
    // Демо (--demo): кадры компонует страница (js/demo.js) и шлёт оболочке
    // по одному; оболочка кодирует GIF (крейт gif) и пишет docs/mira-hero.gif.
    api.demoFrame = (b64, w, h) => inv('demo_frame', { b64: b64, width: w, height: h });
    api.demoDone = (ok) => inv('demo_done', { ok: !!ok });
    // Пульс кадров для сторожа оболочки (BEAT_TIMEOUT): страница зовёт на
    // каждом кадре — команда копеечная, но сторож обязан видеть жизнь.
    api.beat = () => { inv('beat'); };
    api.settings = window.__FRIEND_SETTINGS__ || null;
    // Свежие настройки из файла: снимок из init-скрипта — на момент СОЗДАНИЯ
    // окна, после перезагрузки страницы он тоже устаревает.
    api.getShellSettings = async () => {
      const j = await inv('get_settings');
      if (!j || typeof j !== 'string') return null;
      try { return JSON.parse(j); } catch (_err) { return null; }
    };
    api.saveSettings = (settings) => { inv('save_settings', { json: JSON.stringify(settings) }); };

    // Место окна может НЕ совпасть с тем, куда его просили: оболочка держит
    // окно в видимой части экрана. Настоящее место уходит серверу — тот же
    // контракт report(), что в ветке Electron выше.
    let known = null;
    function report(asked, real) {
      if (!real) return real;
      if (asked && (Math.abs(asked.x - real.x) > 1 || Math.abs(asked.y - real.y) > 1)) {
        if (api.onPositionCorrected) api.onPositionCorrected(real);
      }
      known = { x: real.x, y: real.y };
      return real;
    }

    api.getPosition = async () => {
      const p = await inv('get_position');
      known = { x: p.x, y: p.y };
      return p;
    };
    api.setPosition = async (x, y) => report(
      { x: Math.round(x), y: Math.round(y) },
      await inv('set_position', { x: Math.round(x), y: Math.round(y) }),
    );
    api.moveBy = async (dx, dy) => {
      const asked = known ? { x: Math.round(known.x + dx), y: Math.round(known.y + dy) } : null;
      return report(asked, await inv('move_by', { dx: Math.round(dx), dy: Math.round(dy) }));
    };
    api.setBounds = async (b) => {
      if (b && b.width && b.height) api.size = { width: Math.round(b.width), height: Math.round(b.height) };
      return report(
        b && typeof b.x === 'number' ? { x: Math.round(b.x), y: Math.round(b.y) } : null,
        await inv('set_bounds', { b: { x: b.x, y: b.y, width: b.width, height: b.height } }),
      );
    };
    return api;
  }

  // --- Браузер (dev): окон нет, поэтому двигаем аватар внутри страницы ---
  api.home = homePoint;
  api.setSize = async (w, h) => {
    api.size = { width: w, height: h };
    computeHome();
    notify();
  };
  api.getPosition = async () => ({ x: Math.round(dev.x), y: Math.round(dev.y) });
  api.setPosition = async (x, y) => {
    ensureInit();
    dev.x = x; dev.y = y;
    notify();
    return { x: Math.round(dev.x), y: Math.round(dev.y) };
  };
  api.moveBy = async (dx, dy) => {
    ensureInit();
    dev.moved++;
    dev.x += dx; dev.y += dy;
    notify();
    return { x: Math.round(dev.x), y: Math.round(dev.y) };
  };
  api.setBounds = async (b) => {
    if (b.width && b.height) api.size = { width: b.width, height: b.height };
    return await api.setPosition(b.x, b.y);
  };
  api.setAlwaysOnTop = async () => { /* в браузере нечего включать */ };
  // В браузере сторожа нет — пульс просто ничего не делает (см. ветку Electron).
  api.beat = () => {};
  // Рабочая область в браузере — вся доступная часть экрана: панели задач у
  // страницы нет, и посадка «на панель» совпадёт с низом окна.
  api.workArea = async () => ({
    x: 0,
    y: 0,
    width: window.screen ? window.screen.availWidth : window.innerWidth,
    height: window.screen ? window.screen.availHeight : window.innerHeight,
  });
  api.focus = async () => { window.focus(); };
  api.quit = async () => { window.close(); };
  // onDevMove — подписка для dev-режима (в оболочке не нужна).
  api.onDevMove = (fn) => { onDevMove = fn; };
  return api;
})();
