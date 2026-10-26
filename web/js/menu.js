// menu.js — меню персонажа по правому клику на нём.
//
// Откуда взялась форма меню: у Desktop Mate по правому клику на персонаже
// открывается список действий (сменить персонажа, будильник, настройки, помощь,
// выход), а в настройках первым пунктом идёт РАЗМЕР ползунком; у MateEngine —
// компактное меню с ползунками (размер, частота кадров, сила слежения головой) и
// переключателями «поверх всех», «сидеть на окнах». Здесь та же логика, но без
// чужих настроек, которых у 2D-персонажа нет: размер, поза, куда посадить,
// «смотрит за курсором», «поверх всех» и действия.
//
// Почему окно браузера не подходит для этого меню: у окна компаньона прозрачный
// фон и оно висит поверх всех. Своё системное окно ради меню означало бы третье
// окно, которое надо двигать за персонажем и держать поверх него. Поэтому меню —
// обычная HTML-панель внутри окна персонажа, а окно на время меню расширяется
// (js/main.js, growForMenu): панель не помещается в окно шириной с персонажа.
//
// Настройки живут в localStorage: они не часть состояния сервера (там позиция
// окна), и человек ожидает, что размер сохранится между запусками.
window.FriendMenu = (function () {
  'use strict';

  const KEY = 'friend.settings.v1';

  // Значения по умолчанию: те же, что были до появления меню, чтобы обновление
  // не меняло поведение без спроса.
  const DEFAULTS = {
    pix: 5,              // размер клетки спрайта на экране, пикселей
    seat: 'free',        // free | taskbar | window | edge-left | edge-right
    keepTop: true,       // «поверх всех окон»
    follow: 80,          // сила слежения за курсором, 0..100 (как ползунок силы
                         // слежения у MateEngine): 0 — не следит, 100 — полный
                         // взгляд. Прежнее булево значение из сохранённых
                         // настроек конвертирует pick (true → 80, false → 0)
    voice: true,         // говорит вслух (озвучка реплик, этап 7)
    wake: false,         // слышит её имя фоном (wake word, этап 7): микрофон
                         // при этом включён постоянно, поэтому по умолчанию выкл
    autostart: false,    // запускаться при входе в систему: лезть в реестр
                         // без явного желания человека нельзя, поэтому выкл
    dance: false,        // «реакция на звук»: слушать системный микшер —
                         // лишняя фоновая служба без спроса человека, выкл
    language: '',        // язык персонажа ('' = как в конфиге сервера).
                         // Смена живая (этап 13): страница просит сервер,
                         // сервер пересобирает характер и рассылает lang.
  };

  // Посадки — только идентификаторы: подписи и подсказки живут в словаре
  // (js/lang/*.js) и подставляются при сборке меню и при смене языка.
  const SEATS = [
    { id: 'free' },
    { id: 'taskbar' },
    { id: 'window' },
    { id: 'edge-left' },
    { id: 'edge-right' },
  ];

  // Языки — идентификаторы, подписи в словаре (как посадки).
  const LANGS = [
    { id: 'ru' },
    { id: 'en' },
  ];

  // pick — привести сохранённое к значениям по умолчанию: чужие поля и не тот тип
  // отбрасываем. Испорченная запись — не повод не запускаться.
  const SEAT_KEYS = { free: 1, taskbar: 1, window: 1, 'edge-left': 1, 'edge-right': 1 };

  function pick(saved) {
    const out = Object.assign({}, DEFAULTS);
    if (!saved || typeof saved !== 'object') return out;
    for (const k of Object.keys(DEFAULTS)) {
      if (typeof saved[k] === typeof DEFAULTS[k]) out[k] = saved[k];
    }
    // Сила слежения: ползунок 0..100. Старое булево «смотрит за курсором»
    // превращаем в силу, а не выбрасываем: true → «как раньше» (80),
    // false → выключено (0). Числа правим по краям (314 → 100).
    const f = saved.follow;
    if (typeof f === 'boolean') out.follow = f ? DEFAULTS.follow : 0;
    else if (typeof f === 'number' && isFinite(f)) out.follow = Math.max(0, Math.min(100, Math.round(f)));
    // Посадка: строка из списка. «window» приехало раньше, чем страница
    // научилась на окна сажать, — без проверки на список настройка проходила
    // pick и посадка «висела» несуществующей.
    if (typeof saved.seat !== 'string' || !SEAT_KEYS[saved.seat]) out.seat = DEFAULTS.seat;
    // Язык: только 'ru'/'en' (или '' = как в конфиге). Мусорная строка не
    // должна уезжать на сервер как запрос смены языка.
    if (saved.language !== 'ru' && saved.language !== 'en') out.language = '';
    return out;
  }

  // load — настройки сначала спрашиваем у ОБОЛОЧКИ (файл рядом с состоянием), и
  // только потом у localStorage.
  //
  // Почему так: start.bat гасит прошлое окно СИЛОЙ (taskkill /F), и браузерное
  // хранилище не успевает сброситься на диск — размер и посадка обнулялись при
  // каждом запуске. Человек видел «посадка сбросилась, выхода из-за края нет», хотя
  // ничего не менял (настройки терялись в хранилище, а не в его голове).
  function load(shellSettings) {
    if (shellSettings && typeof shellSettings === 'object') return pick(shellSettings);
    try {
      const raw = window.localStorage.getItem(KEY);
      if (!raw) return Object.assign({}, DEFAULTS);
      return pick(JSON.parse(raw));
    } catch (_err) {
      // Хранилище закрыто (или запись битая) — значения по умолчанию.
      return Object.assign({}, DEFAULTS);
    }
  }

  function save(settings, bridge) {
    try {
      window.localStorage.setItem(KEY, JSON.stringify(settings));
    } catch (_err) { /* приватный режим: настройки просто не переживут перезапуск */ }
    // И файлом у оболочки — именно он переживает перезапуск (см. load выше).
    if (bridge && bridge.saveSettings) bridge.saveSettings(settings);
  }

  function el(tag, cls, text) {
    const node = document.createElement(tag);
    if (cls) node.className = cls;
    if (text !== undefined) node.textContent = text;
    return node;
  }

  function attach(opts) {
    const o = opts || {};
    const avatar = o.avatar;
    const settings = load(o.bridge && o.bridge.settings);
    let open = false;

    // persist — сохранить настройки и в хранилище страницы, и файлом у оболочки.
    function persist() { save(settings, o.bridge); }

    // --- панель ----------------------------------------------------------
    const panel = el('div', 'menu');
    panel.hidden = true;
    panel.setAttribute('role', 'dialog');
    panel.setAttribute('aria-label', friendLang.L('menu.aria'));

    const title = el('div', 'menu-title', (o.name || friendLang.L('menu.title')));
    panel.appendChild(title);

    // Размер — ползунок. Рядом живое число: без него непонятно, насколько
    // «больше» станет персонаж, а окно растёт вместе с ним.
    const sizeRow = el('div', 'menu-row');
    const sizeLabel = el('span', 'menu-label', friendLang.L('menu.sizeLabel'));
    sizeRow.appendChild(sizeLabel);
    const sizeInput = document.createElement('input');
    sizeInput.type = 'range';
    sizeInput.min = String(o.minPix || 2);
    sizeInput.max = String(o.maxPix || 12);
    sizeInput.step = '1';
    sizeInput.value = String(settings.pix);
    sizeInput.setAttribute('aria-label', friendLang.L('menu.sizeAria'));
    const sizeValue = el('span', 'menu-value', settings.pix + ' px');
    sizeRow.appendChild(sizeInput);
    sizeRow.appendChild(sizeValue);
    panel.appendChild(sizeRow);

    // Сила слежения за курсором — второй ползунок (как у MateEngine): не
    // «вкл/выкл», а насколько сильно она провожает курсор глазами. Живое
    // число рядом: «64» видно, и на глаз понятно, половина это силы или нет.
    const followRow = el('div', 'menu-row');
    const followLabel = el('span', 'menu-label', friendLang.L('menu.followLabel'));
    followRow.appendChild(followLabel);
    const followInput = document.createElement('input');
    followInput.type = 'range';
    followInput.min = '0';
    followInput.max = '100';
    followInput.step = '1';
    followInput.value = String(settings.follow);
    followInput.setAttribute('aria-label', friendLang.L('menu.followAria'));
    const followValue = el('span', 'menu-value', settings.follow + ' %');
    followRow.appendChild(followInput);
    followRow.appendChild(followValue);
    panel.appendChild(followRow);

    // Язык (этап 13): две кнопки, подписи всегда на своём языке. Клик —
    // запрос к серверу (onChange → lang), сервер пересобирает характер,
    // голос и wake, и рассылает lang всем окнам.
    const langGroup = el('div', 'menu-label menu-group', friendLang.L('lang.aria'));
    panel.appendChild(langGroup);
    const langRow = el('div', 'menu-seats');
    const langButtons = {};
    for (const lang of LANGS) {
      const btn = el('button', 'menu-seat');
      btn.type = 'button';
      btn.textContent = friendLang.L('lang.' + lang.id);
      btn.addEventListener('click', () => {
        apply('language', lang.id);
      });
      langButtons[lang.id] = btn;
      langRow.appendChild(btn);
    }
    panel.appendChild(langRow);

    // Куда посадить.
    const seatGroup = el('div', 'menu-label menu-group', friendLang.L('menu.groupSeat'));
    panel.appendChild(seatGroup);
    const seatRow = el('div', 'menu-seats');
    const seatButtons = {};
    for (const seat of SEATS) {
      const btn = el('button', 'menu-seat');
      btn.type = 'button';
      btn.textContent = friendLang.L('seat.' + seat.id + '.label');
      btn.title = friendLang.L('seat.' + seat.id + '.hint');
      btn.addEventListener('click', () => {
        apply('seat', seat.id);
      });
      seatButtons[seat.id] = btn;
      seatRow.appendChild(btn);
    }
    panel.appendChild(seatRow);

    // Переключатели.
    const toggles = [
      { id: 'keepTop' },
      { id: 'voice' },
      { id: 'dance' },
      { id: 'wake' },
      { id: 'autostart' },
    ];
    const checkboxes = {};
    const toggleLabels = {};
    for (const t of toggles) {
      const row = el('label', 'menu-check');
      const box = document.createElement('input');
      box.type = 'checkbox';
      box.checked = !!settings[t.id];
      box.addEventListener('change', () => apply(t.id, box.checked));
      row.appendChild(box);
      const label = el('span', '', friendLang.L('toggle.' + t.id));
      row.appendChild(label);
      checkboxes[t.id] = box;
      toggleLabels[t.id] = label;
      panel.appendChild(row);
    }

    // Действия.
    const actions = el('div', 'menu-actions');
    const actionsList = [
      ['action.talk', () => o.onTalk && o.onTalk()],
      ['action.wave', () => o.onWave && o.onWave()],
      ['action.home', () => o.onHome && o.onHome()],
      ['action.memory', () => o.onMemory && o.onMemory()],
      ['action.quit', () => o.onQuit && o.onQuit()],
    ];
    const actionButtons = [];
    for (const [key, fn] of actionsList) {
      const btn = el('button', 'menu-action', friendLang.L(key));
      btn.type = 'button';
      btn.addEventListener('click', () => {
        close();
        fn();
      });
      actionButtons.push({ btn: btn, key: key });
      actions.appendChild(btn);
    }
    panel.appendChild(actions);

    const hint = el('div', 'menu-hint', friendLang.L('menu.hint'));
    panel.appendChild(hint);

    document.body.appendChild(panel);

    // --- применение настроек ---------------------------------------------
    function apply(key, value, quiet) {
      settings[key] = value;
      persist();
      sync();
      // Каждая настройка — это действие страницы (размер окна, поза, позиция).
      // Меню только сообщает «что выбрал человек», а как это сделать, знает
      // js/main.js: там геометрия окна и посадка.
      if (o.onChange) o.onChange(key, value);
      if (!quiet) {
        // Панель остаётся открытой: размер смотрят глазами и правят ещё раз.
        if (key === 'seat') sizeInput.blur();
      }
    }

    // sync — привести вид меню к настройкам. Нужно не только при выборе: размер
    // меняется ещё и колесом мыши по персонажу (js/main.js), и тогда ползунок
    // обязан это показать — иначе он показывал бы неправду.
    function sync() {
      sizeInput.value = String(settings.pix);
      sizeValue.textContent = settings.pix + ' px';
      followInput.value = String(settings.follow);
      followValue.textContent = settings.follow + ' %';
      for (const id of Object.keys(seatButtons)) {
        const on = settings.seat === id;
        seatButtons[id].classList.toggle('on', on);
        seatButtons[id].setAttribute('aria-pressed', on ? 'true' : 'false');
      }
      for (const id of Object.keys(checkboxes)) checkboxes[id].checked = !!settings[id];
      // Кнопки языка: подсвечиваем ТЕКУЩИЙ язык страницы ('' = серверный из
      // конфига, он же язык словаря) — меню не показывает выбранным то,
      // чего нет.
      const effLang = settings.language || friendLang.code();
      for (const id of Object.keys(langButtons)) {
        const on = effLang === id;
        langButtons[id].classList.toggle('on', on);
        langButtons[id].setAttribute('aria-pressed', on ? 'true' : 'false');
      }
    }

    sizeInput.addEventListener('input', () => apply('pix', Number(sizeInput.value)));
    followInput.addEventListener('input', () => apply('follow', Number(followInput.value)));
    sync();

    // --- открыть/закрыть -------------------------------------------------
    function isOpen() { return open; }

    function openMenu() {
      if (open) return;
      open = true;
      sync();
      if (o.onOpen) o.onOpen();
      panel.hidden = false;
      document.body.classList.add('menu-open');
      // Окно поверх всех и часто не в фокусе: без явного фокуса ползунок не
      // сдвинулся бы стрелками, а Esc не закрыл бы меню.
      if (o.bridge && o.bridge.focus) o.bridge.focus();
    }

    function close() {
      if (!open) return;
      open = false;
      panel.hidden = true;
      document.body.classList.remove('menu-open');
      if (o.onClose) o.onClose();
    }

    function toggle() { if (open) close(); else openMenu(); }

    // Панель — обычный элемент страницы, поэтому «мимо панели» ловится по
    // pointerdown: правый клик по персонажу открывает, повторный — закрывает.
    window.addEventListener('pointerdown', (ev) => {
      if (!open) return;
      if (panel.contains(ev.target)) return;
      close();
    }, true);

    window.addEventListener('keydown', (ev) => {
      if (!open) return;
      if (ev.key === 'Escape') {
        ev.preventDefault();
        close();
        // Esc в поле ввода разговора тоже закрывает окно ввода — здесь он уже
        // обработан, поэтому дальше событие не пускаем.
        ev.stopPropagation();
      }
    }, true);

    return {
      isOpen: isOpen,
      open: openMenu,
      close: close,
      toggle: toggle,
      settings: function () { return Object.assign({}, settings); },
      // setPix — внешнее изменение размера (колесо мыши по персонажу): ползунок
      // должен показать новое число, а не остаться на прежнем.
      setPix: function (pix) {
        settings.pix = pix;
        persist();
        sync();
      },
      // setToggle — внешняя правка ПЕРЕКЛЮЧАТЕЛЯ (boolean): оболочка откатила
      // автозапуск, страница сняла галку. Меню не показывает то, чего нет.
      setToggle: function (key, value) {
        if (typeof DEFAULTS[key] !== 'boolean') return;
        settings[key] = !!value;
        persist();
        sync();
      },
      // setSeatQuiet — внешняя смена посадки БЕЗ эффекта меню: записывает и
      // перерисовывает, но НЕ зовёт onChange. Для страницы это важно: у
      // onChange-посадки переезд окна стоит в очереди growForMenu, и если
      // посадку применить прямо во время меню, переезд в эту очередь попадёт
      // и будет исполнен только после его закрытия.
      setSeatQuiet: function (id) {
        settings.seat = id;
        persist();
        sync();
      },
      // setSeat — посадку меняет не только меню: человека мог унести её мышью, и
      // тогда меню обязано показать «свободно», а не прошлый выбор.
      setSeat: function (id) {
        settings.seat = id;
        persist();
        sync();
      },
      setName: function (name) {
        if (name) title.textContent = name;
      },
      // setLanguage — после welcome с другим языком: панель собрана один раз,
      // поэтому подписи перезаписываем на месте. Название персоны остаётся
      // (его пришёл welcome), настройки не трогаем.
      setLanguage: function () {
        panel.setAttribute('aria-label', friendLang.L('menu.aria'));
        sizeLabel.textContent = friendLang.L('menu.sizeLabel');
        sizeInput.setAttribute('aria-label', friendLang.L('menu.sizeAria'));
        followLabel.textContent = friendLang.L('menu.followLabel');
        followInput.setAttribute('aria-label', friendLang.L('menu.followAria'));
        seatGroup.textContent = friendLang.L('menu.groupSeat');
        for (const seat of SEATS) {
          seatButtons[seat.id].textContent = friendLang.L('seat.' + seat.id + '.label');
          seatButtons[seat.id].title = friendLang.L('seat.' + seat.id + '.hint');
        }
        for (const t of toggles) toggleLabels[t.id].textContent = friendLang.L('toggle.' + t.id);
        for (const a of actionButtons) a.btn.textContent = friendLang.L(a.key);
        langGroup.textContent = friendLang.L('lang.aria');
        for (const lang of LANGS) langButtons[lang.id].textContent = friendLang.L('lang.' + lang.id);
        hint.textContent = friendLang.L('menu.hint');
      },
      // setLang — язык поменялся НЕ от клика по меню: сервер прислал lang
      // (окно могло быть второе, язык мог смениться на старте по сохранённой
      // настройке). Записываем и перекрашиваем кнопки.
      setLang: function (code) {
        if (code !== 'ru' && code !== 'en') return;
        settings.language = code;
        persist();
        sync();
      },
      panel: panel,
    };
  }

  return { attach: attach, defaults: DEFAULTS, seats: SEATS };
})();
