// preload.js — мост оболочки в страницу персонажа.
//
// Наружу отдаётся РОВНО то, что нужно компаньону, и ничего больше: страница не
// получает ни доступа к файлам, ни к процессам. Каждый вызов — именованный
// канал в main.js, поэтому расширение возможностей оболочки — это явное
// добавление метода, а не «включим nodeIntegration».
const { contextBridge, ipcRenderer } = require('electron');

// Настройки персонажа (размер, посадка, переключатели) оболочка передаёт
// аргументом командной строки: так страница видит их СРАЗУ при загрузке, а не
// получает вдогонку после первого кадра. Почему не localStorage: start.bat гасит
// прошлое окно силой, и записи не успевают сброситься на диск — размер и посадка
// обнулялись при каждом запуске (см. desktop/main.js, «Настройки персонажа»).
const PREFIX = '--friend-settings=';
const rawSettings = (process.argv.find((a) => a && a.indexOf(PREFIX) === 0) || '').slice(PREFIX.length);
let storedSettings = {};
try {
  storedSettings = rawSettings ? JSON.parse(rawSettings) : {};
} catch (_err) {
  // Испорченная строка — работаем на значениях по умолчанию, а не падаем на
  // старте: настройки не стоят непоказанного персонажа.
  storedSettings = {};
}

// ИМЕНА МЕТОДОВ ЗДЕСЬ — ЧАСТЬ КОНТРАКТА: страница зовёт ровно их
// (web/js/bridge.js). Пока здесь стоял moveWindow, а страница звала moveBy,
// перетаскивание молча не работало: promise отклонялся, окно не двигалось, в
// логе — ничего. Поэтому набор методов проверяется тестом оболочки
// (desktop/main.js, -selftest), а не взглядом.
contextBridge.exposeInMainWorld('friendNative', {
  moveBy: (dx, dy) => ipcRenderer.invoke('win:move-by', dx, dy),
  setBounds: (b) => ipcRenderer.invoke('win:set-bounds', b),
  getPosition: () => ipcRenderer.invoke('win:get-position'),
  setPosition: (x, y) => ipcRenderer.invoke('win:set-position', x, y),
  setSize: (w, h) => ipcRenderer.invoke('win:set-size', w, h),
  setAlwaysOnTop: (on) => ipcRenderer.invoke('win:always-on-top', on),
  // workArea — рабочая область экрана без панели задач: по ней страница ставит
  // персонажа на панель задач (js/main.js, applySeat).
  workArea: () => ipcRenderer.invoke('screen:work-area'),
  // focus: окно компаньона висит поверх всех и может быть не в фокусе. Без
  // этого набранный в поле ввода текст ушёл бы в программу под окном.
  focus: () => ipcRenderer.invoke('win:focus'),
  quit: () => ipcRenderer.invoke('app:quit'),
  // Автозапуск при входе в систему: invoke, а не send — странице важен ответ
  // «применилось ли», меню показывает правдивый чекбокс.
  autostart: (on) => ipcRenderer.invoke('app:autostart', on),
  // id дорожки системного микшера для «танцует под музыку» (js/music.js):
  // invoke — странице нужен ответ (источника может не быть).
  audioSource: () => ipcRenderer.invoke('app:audio-source'),
  // Настройки: прочитанные при старте и запись при изменении. Канал send: они
  // сохраняются на каждое движение ползунка, ответ странице не нужен.
  settings: storedSettings,
  saveSettings: (json) => ipcRenderer.send('app:save-settings', json),
  // beat — пульс: страница говорит «кадр нарисован». Это НЕ таймер страницы, а
  // сам кадр (см. web/js/main.js: тикер PixiJS), поэтому молчание пульса
  // означает ровно одно: картинки больше нет. Оболочка по нему возвращает
  // персонажа, если отрисовка умерла (драйвер, потеря контекста WebGL).
  // Канал send, а не invoke: пульс летит каждый кадр (60 раз в секунду), и ждать
  // на него ответ было бы лишней работой.
  beat: () => ipcRenderer.send('page:beat'),
  // onFocus — страница подписывается на «у человека сменилось активное окно»
  // (данные приходят от помощника focuswatch через оболочку). Это send-поток:
  // события идут, когда человек переключает окна, ответов не требуется.
  onFocus: (cb) => {
    if (typeof cb === 'function') ipcRenderer.on('focus:info', (_event, data) => cb(data));
  },
  // dance(on) — включить/выключить помощник soundwatch (WASAPI-лупбек
  // микшера). onSound — подписка на RMS-уровень, который помощник присылает
  // через оболочку. Приватность: наружу от звука уходит ровно одно число,
  // и только в локальную страницу; содержимое звука не сохраняется.
  dance: (on) => ipcRenderer.invoke('app:dance', !!on),
  onSound: (cb) => {
    if (typeof cb === 'function') ipcRenderer.on('sound:level', (_event, level) => cb(level));
  },
});
