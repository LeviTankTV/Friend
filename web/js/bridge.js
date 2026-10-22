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

  // --- Tauri: window.__TAURI__ появляется из @tauri-apps/api ---
  if (window.__TAURI__) {
    api.mode = 'tauri';
    api.isNative = true;
    const t = window.__TAURI__;
    const appWindow = t.window && t.window.getCurrent ? t.window.getCurrent() : null;
    api.home = homePoint;
    api.setSize = async (w, h) => { if (appWindow) await appWindow.setSize(new t.window.LogicalSize(w, h)); };
    api.getPosition = async () => {
      if (!appWindow) return { x: 0, y: 0 };
      const p = await appWindow.outerPosition();
      const s = await appWindow.scaleFactor();
      return { x: Math.round(p.x / s), y: Math.round(p.y / s) };
    };
    api.setPosition = async (x, y) => {
      if (!appWindow) return { x, y };
      await appWindow.setPosition(new t.window.LogicalPosition(x, y));
      return { x, y };
    };
    api.moveBy = async (dx, dy) => {
      const p = await api.getPosition();
      return await api.setPosition(Math.round(p.x + dx), Math.round(p.y + dy));
    };
    api.setBounds = async (b) => {
      await api.setSize(b.width, b.height);
      return await api.setPosition(b.x, b.y);
    };
    api.setAlwaysOnTop = async (v) => { if (appWindow) await appWindow.setAlwaysOnTop(v); };
    api.focus = async () => { if (appWindow) await appWindow.setFocus(); };
    api.quit = async () => { if (window.__TAURI__ && t.process) t.process.exit(0); };
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
