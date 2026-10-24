// main.js — сборка окна компаньона: PixiJS сцена, персонаж, перетаскивание,
// разговор, меню (правый клик) и связь с сервером.
//
// Порядок работы:
//   1) поднимаем PixiJS с прозрачным фоном;
//   2) рисуем персонажа (из сетки 40×56, см. js/avatar.js);
//   3) подключаемся к /ws и получаем welcome → сохранённое состояние;
//   4) ставим окно туда, где оно было в прошлый раз;
//   5) перетаскивание → позиция уезжает на сервер.
//
// Геометрия окна (важно для всего остального):
//
//   ┌──────────────────────────┐  ← верх окна
//   │      облачко реплик      │  PAD_TOP (место над головой)
//   │         ╭────╮           │
//   │         │она │           │  спрайт
//   │         ╰────╯           │
//   │          ~~~~            │  PAD_BOTTOM (место под тень)
//   └──────────────────────────┘  ← низ окна = «нога» персонажа
//
// Окно БОЛЬШЕ спрайта, и это принципиально: облачку реплик нужно место, а расти
// ему некуда, если окно впритык по фигуре — именно поэтому реплики «уезжали» и
// растягивались. Поля постоянные, поэтому окно остаётся ограниченным по размеру
// при любой длине реплики: длинный текст прокручивается, а не растягивает окно.
//
// Точка отсчёта — НЕ центр canvas, а «ноги» персонажа на экране (anchor). Пока
// персонаж стоял по центру окна, любое изменение окна (размер, расширение под
// меню, смена экрана) сдвигало его самого. Якорь на ногах делает его
// независимым от размера окна и делает посадку на панель задач простой
// арифметикой: anchor.y = верх панели задач.
//
// Правило, которое здесь важно: показания о позиции берём У ОБОЛОЧКИ
// (bridge.getPosition), а не у своего счётчика. Иначе после перезапуска сервера
// или переезда окна мышью наша «правда» разъедется с экраном.
(function () {
  'use strict';

  const bridge = window.FriendBridge;
  const net = window.FriendNet;

  // Поля окна вокруг персонажа (см. схему выше).
  const PAD_X = 24;
  const PAD_TOP = 84;
  const PAD_BOTTOM = 10;
  // Ширина полосы, которую занимает меню, когда оно открыто.
  const MENU_W = 252;

  const el = {
    stage: document.getElementById('stage'),
    dot: document.getElementById('dot'),
    name: document.getElementById('name'),
    hint: document.getElementById('hint'),
  };

  // applyLang — надписи каркаса (заголовок окна, подсказка, aria) из словаря.
  // Вызывается на старте (язык по умолчанию — ru) и снова из welcome, когда
  // сервер назовёт язык конфига.
  let personaName = friendLang.L('chat.defaultName');
  function applyLang() {
    document.title = friendLang.L('app.title');
    el.stage.setAttribute('aria-label', friendLang.L('app.stageAria'));
    el.dot.title = friendLang.L('hud.linkTitle');
    if (el.hint) el.hint.textContent = friendLang.L('hud.hint');
    wakeDot.title = friendLang.L('wake.dotTitle', { name: personaName });
  }

  // Размер canvas задаём ЯВНО из контейнера, а не через resizeTo: window.
  // resizeTo пересчитывает размер только по событию resize, и если страница
  // успела загрузиться до того, как окно получило размер (или панель была
  // свёрнута), canvas остаётся 0×0 — персонажа просто нет, хотя всё «работает».
  const app = new PIXI.Application({
    backgroundAlpha: 0,          // прозрачное окно: сквозь него виден рабочий стол
    antialias: true,
    resolution: Math.min(2, window.devicePixelRatio || 1),
    autoDensity: true,
    width: 1,
    height: 1,
  });
  el.stage.appendChild(app.view);

  // Индикатор фонового слушания её имени (wake word, этап 7): пока слушатель
  // работает, точка горит — человек обязан видеть, что микрофон открыт.
  // Подпись точки зависит от языка и имени — обновляется applyLang.
  const wakeDot = document.createElement('div');
  wakeDot.className = 'wake-ind';
  wakeDot.hidden = true;
  document.body.appendChild(wakeDot);
  applyLang();

  // Оболочка может поставить окно НЕ туда, куда её попросили: она держит его в
  // видимой части рабочего стола. Например, сохранённая позиция оказалась за
  // пределами экрана (монитор отключили, разрешение поменяли, координаты
  // записались битыми) — тогда окно возвращают на экран, а настоящее место
  // уезжает серверу. Без этого в памяти остался бы несуществующий адрес, и окно
  // уезжало бы в никуда при каждом запуске — а достать его мышью уже нельзя.
  if (bridge.onPositionCorrected) {
    bridge.onPositionCorrected((p) => net.sendPosition(p.x, p.y));
  }

  function canvasSize() {
    return {
      w: app.renderer.width / app.renderer.resolution,
      h: app.renderer.height / app.renderer.resolution,
    };
  }

  function fitCanvas() {
    const r = el.stage.getBoundingClientRect();
    const w = Math.max(1, Math.round(r.width || window.innerWidth || 320));
    const h = Math.max(1, Math.round(r.height || window.innerHeight || 420));
    if (Math.round(app.renderer.width / app.renderer.resolution) !== w ||
        Math.round(app.renderer.height / app.renderer.resolution) !== h) {
      app.renderer.resize(w, h);
    }
  }

  const avatar = window.FriendAvatar.create(PIXI, {
    width: bridge.size.width,
    height: bridge.size.height,
  });
  avatar.container.eventMode = 'static';   // мышь ловит ТОЛЬКО персонаж
  avatar.container.cursor = 'grab';
  app.stage.addChild(avatar.container);

  // --- геометрия: где она на экране ----------------------------------------

  // winPos — левый верхний угол окна на экране, anchor — её «ноги» на экране
  // (низ спрайта по центру). Обе величины в пикселях экрана; в браузере «экран» —
  // это страница, а угол окна — ноль.
  let winPos = { x: 0, y: 0 };
  let anchor = { x: 0, y: 0 };
  let devOffset = { x: 0, y: 0 };   // dev-режим браузера: сдвиг «окна» внутри страницы
  let baseCx = 0;     // где спрайт стоял бы без «выхода» из-за края экрана
  let peekDx = 0;     // насколько спрайт сдвинут этим выходом прямо сейчас

  function windowSize() {
    return {
      w: Math.round(avatar.width) + PAD_X * 2,
      h: Math.round(avatar.height) + PAD_TOP + PAD_BOTTOM,
    };
  }

  // windowPosForAnchor — где должно стоять окно, чтобы её ноги оказались в
  // anchor. Обратная операция к anchorFromWindow; вместе они дают «персонаж не
  // двигается, когда меняется окно».
  function windowPosForAnchor() {
    return {
      x: Math.round(anchor.x - PAD_X - avatar.width / 2),
      y: Math.round(anchor.y - PAD_TOP - avatar.height),
    };
  }

  function anchorFromWindow(pos) {
    winPos = { x: pos.x, y: pos.y };
    anchor = {
      x: pos.x + PAD_X + avatar.width / 2,
      y: pos.y + PAD_TOP + avatar.height,
    };
  }

  function layout() {
    fitCanvas();
    const { w, h } = canvasSize();
    // В браузере окна нет: «угол окна» — начало страницы плюс сдвиг dev-режима.
    const originX = bridge.isNative ? winPos.x : devOffset.x;
    const originY = bridge.isNative ? winPos.y : devOffset.y;
    const cx = bridge.isNative ? anchor.x - originX : w / 2 + devOffset.x;
    // y спрайта — это его ЦЕНТР, а anchor — её НОГИ (низ спрайта). Здесь раньше
    // стояло cy = anchor.y - originY, то есть центр ставился туда, где должны
    // быть ноги: половина спрайта (140 px при размере 5) уезжала ниже нижней
    // кромки окна. На экране это выглядело как «видно только голову и пряди,
    // тела нет», а посадка на панель задач читалась как «висит в воздухе над
    // панелью»: ноги-то были за кромкой. Отнимаем половину спрайта — и её низ
    // (anchor) совпадает с низом спрайта.
    const cy = bridge.isNative ? anchor.y - originY - avatar.height / 2 : h / 2 + devOffset.y;
    baseCx = cx;
    avatar.container.x = cx + peekDx;
    avatar.setBaseY(cy);
    // Поля окна облачко реплик узнаёт отсюда: держать одни и те же числа в CSS и
    // в js — верный способ разъехаться при первой же правке геометрии.
    document.documentElement.style.setProperty('--pad-top', PAD_TOP + 'px');
    document.documentElement.style.setProperty('--pad-x', PAD_X + 'px');
    // Размер, который окно ДОЛЖНО иметь (спрайт плюс поля). По нему считают свою
    // ширину облачко и поле ввода (css/overlay.css): интерфейс не должен
    // растягиваться шире персонажа, даже если окно окажется больше, чем просили.
    document.documentElement.style.setProperty('--ui-w', windowSize().w + 'px');
  }

  // В dev-режиме мост сообщает, на сколько «окно уехало от домашней точки» —
  // двигаем аватар в странице. Смещение показываем в пределах экрана: позиция,
  // сохранённая на другом мониторе, не должна унести персонажа за край и
  // оставить пустое окно.
  if (bridge.onDevMove) {
    bridge.onDevMove((dx, dy) => {
      const { w, h } = canvasSize();
      const limX = Math.max(0, w / 2 - bridge.size.width / 2);
      const limY = Math.max(0, h / 2 - bridge.size.height / 2);
      devOffset = {
        x: Math.max(-limX, Math.min(limX, dx)),
        y: Math.max(-limY, Math.min(limY, dy)),
      };
      layout();
    });
  }

  window.addEventListener('resize', layout);
  if (window.ResizeObserver) {
    new ResizeObserver(() => layout()).observe(el.stage);
  }
  layout();

  // Потеря контекста WebGL — вторая жизнь одной и той же поломки «персонаж
  // исчез»: окно на месте, процесс жив, на экране пусто, а ошибок в консоли
  // может не быть вовсе. Своими силами контекст не поднимается — страницу надо
  // перезагрузить (сделать это может и сторож оболочки по пульсу, см. ниже), но
  // перезагружаем сразу: ждать ради этого три секунды незачем.
  app.view.addEventListener('webglcontextlost', (ev) => {
    ev.preventDefault();
    console.error('[friend] контекст WebGL потерян — перезагружаю страницу');
    location.reload();
  });

  // --- выход из-за края экрана ---------------------------------------------
  //
  // Посадили её к краю — окно встаёт КРОМКОЙ ровно к кромке экрана, а видно её
  // или нет, решает доля выхода (js/peek.js): 0 — она целиком за кромкой, 1 —
  // вышла и стоит на виду. Сдвигаем при этом не окно, а спрайт внутри него по
  // двум причинам. Первая: кромка окна совпадает с краем экрана, поэтому «стена»,
  // из-за которой она выглядывает, стоит на месте, и обрезку делает сама картинка
  // — ровно по краю экрана. Вторая: движение остаётся плавным, потому что это
  // отрисовка, а не переезд окна через IPC шестьдесят раз в секунду.
  const PEEK_MARGIN = 8;   // насколько прятаться за кромку «с запасом», пикселей

  const peek = window.FriendPeek.create({
    // Её нельзя прятать посреди разговора: открытый ввод или идущий ответ
    // означают, что человек с ней говорит.
    holdOut: () => !!(window.friendChat &&
      (window.friendChat.isOpen() || window.friendChat.isThinking())),
    onWave: () => { if (avatar.wave) avatar.wave(); },
  });

  // peekSpan — сколько ей нужно пройти, чтобы спрятаться целиком: её центр уходит
  // от центра окна за кромку на половину спрайта, плюс поле окна и запас.
  function peekSpan() { return avatar.width + PAD_X + PEEK_MARGIN; }

  // syncPeek — посчитать смещение по состоянию выхода и подвинуть спрайт.
  function syncPeek() {
    const dir = peek.dir();
    const next = dir ? Math.round(dir * peekSpan() * (1 - peek.out())) : 0;
    if (next === peekDx) return;
    peekDx = next;
    avatar.container.x = baseCx + peekDx;
  }

  function tickPeek(dt) {
    peek.tick(dt);
    syncPeek();
  }

  // --- настройки: размер, посадка, переключатели ---------------------------

  const menu = window.FriendMenu.attach({
    avatar: avatar,
    bridge: bridge,
    minPix: window.FriendAvatar.minPix,
    maxPix: window.FriendAvatar.maxPix,
    onChange: (key, value) => {
      if (key === 'pix') applySize(value);
      if (key === 'seat') applySeat(value);
      if (key === 'keepTop' && bridge.setAlwaysOnTop) bridge.setAlwaysOnTop(value);
      if (key === 'follow') applyFollow(value);
      if (key === 'dance') applyDance(value);
      // Автозапуск — дело оболочки (ключ автозапуска Windows). Если применить
      // не удалось, снимаем галку: меню не должно показывать то, чего нет.
      if (key === 'autostart' && bridge.setAutostart) {
        Promise.resolve(bridge.setAutostart(value)).then((applied) => {
          if (!applied && menu.settings().autostart === value) menu.setToggle('autostart', !value);
        });
      }
    },
    onOpen: () => growForMenu(true),
    onClose: () => growForMenu(false),
    onTalk: () => window.friendChat && window.friendChat.toggle(),
    onWave: () => avatar.wave(),
    onHome: () => goHome(),
    onQuit: () => { if (bridge.quit) bridge.quit(); },
    name: '',
  });
  document.body.classList.add('menu-ready');

  // applySize — размер персонажа. Спрайт масштабируется целиком (сетка та же),
  // окно подгоняется под новый размер, а её ноги остаются на месте: иначе
  // персонаж «прыгал» бы при каждом движении ползунка.
  function applySize(pix) {
    avatar.setScale(pix);
    menu.setPix(Math.max(window.FriendAvatar.minPix, Math.min(window.FriendAvatar.maxPix, Math.round(pix))));
    const size = windowSize();
    const pos = windowPosForAnchor();
    Promise.resolve(bridge.setBounds({ x: pos.x, y: pos.y, width: size.w, height: size.h }))
      .then((real) => {
        if (real && typeof real.x === 'number') anchorFromWindow(real);
        layout();
      });
  }

  // applyFollow — смотрит ли она за курсором. Отключаем, потому что это первое,
  // что начинает раздражать, когда персонаж открыт весь день.
  // Сила слежения за курсором (0..1): ползунок в меню. 0 — не следит вовсе,
  // 1 — провожает курсор глазами на всю доступную величину (зрачки в avatar.js
  // упираются в край глазницы, не дальше).
  let followPower = 0.8;
  function applyFollow(v) {
    // Старое булево (сохранённое до появления ползунка) тоже понимает:
    // true → «как раньше», false → выключено.
    let p;
    if (typeof v === 'boolean') p = v ? 0.8 : 0;
    else p = Math.max(0, Math.min(100, Math.round(Number(v) || 0))) / 100;
    followPower = p;
    if (p === 0) avatar.lookAt(0, 0); // взгляд возвращается к середине
  }
  function applyKeepTop(on) { if (bridge.setAlwaysOnTop) bridge.setAlwaysOnTop(!!on); }

  // --- облачко реплик и разговор -------------------------------------------

  const chat = window.FriendChat.create({
    net: net,
    bridge: bridge,
    avatar: avatar,
    // Любая реплика (её ответ, слова человека, ошибка) — повод выглянуть: у края
    // экрана ответ иначе приходит в пустоту, за кромку.
    onActivity: () => peek.poke(),
    // Озвучка её реплик (этап 7): chat передаёт текст, решение «говорить ли»
    // здесь — availability из welcome и переключатель меню.
    speak: speak,
    // Голосовой ввод (этап 7): chat записала микрофон — расшифровкой здесь
    // (WAV 16 кГц → /stt), кнопка показывается по stt из welcome.
    transcribe: transcribe,
  });
  window.friendChat = chat;

  // Курсор рядом с кромкой — она выглядывает. У края экрана иначе её не достать,
  // а ждать случайной паузы человек не станет: подвёл мышь — она показалась.
  // (Заодно движение мыши по её окну — событие «человек рядом» для idlewatch:
  // система считает любой ввод, но её окно её же курсор и подтверждает.)
  window.addEventListener('pointermove', () => { peek.poke(); idleTouch(); });

  window.addEventListener('pointermove', (ev) => {
    if (followPower <= 0) return;
    const rect = app.view.getBoundingClientRect();
    // Зрачки: цель в координатах от центра спрайта. Смещение ограничивает сам
    // аватар (см. avatar.js, lookAt): больше одного пикселя спрайта — и это уже
    // не взгляд, а вывернутые глаза. Сила ползунка масштабирует, насколько
    // зрачки доезжают до этого предела.
    avatar.lookPower(ev.clientX - rect.left - avatar.container.x,
      ev.clientY - rect.top - avatar.container.y, followPower);
  });

  // --- перетаскивание и поглаживание --------------------------------------

  let drag = null;

  function isOverAvatar(clientX, clientY) {
    const rect = app.view.getBoundingClientRect();
    return avatar.hitTest(clientX - rect.left - avatar.container.x,
      clientY - rect.top - avatar.container.y);
  }

  const pet = window.FriendPet.attach({
    target: el.stage,
    view: app.view,
    avatar: avatar,
    bridge: bridge,
    isDragging: () => drag && drag.isDragging(),
  });

  // «Реакция на звук» (этап 7): танцует под музыку, которая играет на
  // колонках. Уровень читает Go-помощник soundwatch (WASAPI-лупбек микшера,
  // см. server/cmd/soundwatch): из сигнала наружу живёт РОВНО RMS-число,
  // звук нигде не сохраняется и никуда не уходит. Её собственный голос
  // (TTS из тех же колонок) модуль считает музыкой с ровным темпом — она
  // танцует под себя, как у Desktop Mate.
  const music = window.FriendMusic.attach({
    setCapture: (on) => bridge.setDanceCapture(on),
    isSelf: () => ttsPlaying > 0,
    onState: (s) => {
      // Темп из измеренных промежутков между пиками (рад/с на подскок).
      const w = s.bpm ? (Math.PI * s.bpm) / 30 : 6.3;
      avatar.setDance(s.dancing, w);
      if (s.dancing) {
        idle.touch();   // музыка играет — человек точно рядом, не спать
        // Танцует — радуется; спящую будит (setEmotion сам снимает сон).
        if (avatar.emotion() !== 'happy') avatar.setEmotion('happy');
      } else if (avatar.emotion() === 'happy') {
        avatar.setEmotion(avatar.calmFace());
      }
    },
  });
  // applyDance — переключатель «танцует под музыку»: старт/стоп помощника.
  // start() асинхронный и never throws: нет помощника — в state() лежит
  // причина, танца просто нет (браузер без оболочки, машина без звука).
  function applyDance(on) {
    if (on) music.start();
    else music.stop();
  }
  // RMS-числа от soundwatch (через оболочку): в тестовом режиме feed-уровни
  // важнее живых — самопроверка не зависит от того, что реально играет.
  bridge.onSound((lv) => music.pushLevel(lv));

  // --- «Сон/уход после простоя» (js/idlewatch.js) --------------------------
  // 10 минут без человека — засыпает («Zzz»); 30 минут — «уходит» к ближайшему
  // краю экрана и спит там, выглядывая; вернулся — оживает и возвращается на
  // прежнюю посадку. Простой считает сама страница: idle из кадров focuswatch
  // плюс местные события (мышь по ней, клик, чат, меню, её голос, танец).
  let idlePrevSeat = null;
  let idlePrevPos = null;
  const idle = window.FriendIdle.attach({
    busy: () => menu.isOpen() || chat.isOpen() || (drag && drag.isDragging()) ||
      avatar.walking() !== 0 || avatar.dancing() || ttsPlaying > 0,
    onSleep: () => { avatar.setEmotion('sleep'); },
    onWake: () => { if (avatar.emotion() === 'sleep') avatar.setEmotion(avatar.calmFace()); },
    // Уход: на прежнюю посадку не претендуем — везём к ближайшему краю
    // (тот же край-механизм с выглядыванием, что в меню). Прежние посадку и
    // место запоминаем, чтобы вернуться ровно туда, где стояли.
    onLeave: () => {
      idlePrevSeat = menu.settings().seat;
      Promise.all([bridge.getPosition(), bridge.workArea()]).then((p) => {
        const pos = p[0]; const wa = p[1] || { x: 0, width: window.screen.availWidth };
        if (pos && typeof pos.x === 'number') idlePrevPos = { x: pos.x, y: pos.y };
        const mid = wa.x + wa.width / 2;
        const center = pos && typeof pos.x === 'number' ? pos.x + windowSize().w / 2 : mid;
        applySeat(center < mid ? 'edge-left' : 'edge-right');
      });
    },
    onReturn: () => {
      const s = idlePrevSeat || 'free';
      const pos = idlePrevPos;
      idlePrevSeat = null;
      idlePrevPos = null;
      Promise.resolve(applySeat(s)).then(() => {
        // applySeat('free') стоит там, где anchor: уход сдвинул anchor к краю,
        // поэтому «на прежнее место» везём окном явно.
        if (pos) {
          const size = windowSize();
          return bridge.setBounds({ x: pos.x, y: pos.y, width: size.w, height: size.h })
            .then((real) => { if (real && typeof real.x === 'number') anchorFromWindow(real); });
        }
      });
      if (menu.settings().seat !== s) menu.setSeatQuiet(s);
    },
  });
  // Её реплики иwake-ответы не будят её саму на пустом месте: голос из
  // колонок — событие «рядом что-то было» только в узком смысле; сон от
  // серверной команды «иди спать» (emotion sleep) не отменяем: его будит
  // только возвращение человека.
  function idleTouch() { idle.touch(); }

  drag = window.FriendDrag.attach(el.stage, bridge, {
    // Ловим мышь только на персонаже: считаем попадание в того же персонажа, что
    // назначен hitArea аватара (см. avatar.js).
    hitTest: isOverAvatar,
    // Клик по персонажу (мышь не сдвинулась) — это не переезд окна: он открывает
    // разговор, а она машет рукой.
    onClick: () => {
      if (menu.isOpen()) return;      // меню открыто — клик по ней его закрывает
      idleTouch();                    // кликнул по ней — человек рядом
      pokeSpeak();
      avatar.wave();
      chat.toggle();
    },
    onStart: () => { stopWalk('её взяли мышью'); idleTouch(); },
    onEnd: (pos, movedPx) => {
      if (movedPx < 4) return;        // это был клик, а не перетаскивание
      stopWalk('её перетащили');
      anchorFromWindow(pos);
      net.sendPosition(pos.x, pos.y);
      seatDwell = 0;
      // Усадила УДЕРЖАНИЕМ (посреди протяжки) — это выбор человека, посадку
      // не снимаем; снятая ниже логика «унесла мышью — значит free» работает
      // ровно для обычных переездов.
      const justSat = seatAppliedAt && Date.now() - seatAppliedAt < 4000 &&
        menu.settings().seat === 'window';
      if (justSat) {
        if (menu.isOpen()) menu.close();
      } else {
        // Меню закрывается ПОСЛЕ переезда, а не на его начале: пока оно открыто,
        // окно шире, и закрытие на старте дёрнуло бы окно прямо под курсором —
        // персонаж уехал бы вбок вместе с протяжкой.
        //
        // Человек унёс её мышью — значит, посадку выбирает он, а не меню: иначе
        // следующая посадка вернула бы её на панель задач.
        if (menu.isOpen() || menu.settings().seat !== 'free') {
          menu.setSeat('free');
          menu.close();
        }
      }
    },
  });

  // --- колесо мыши: размер (как в MateEngine) ------------------------------

  window.addEventListener('wheel', (ev) => {
    if (!isOverAvatar(ev.clientX, ev.clientY)) return;
    ev.preventDefault();
    const step = ev.deltaY < 0 ? 1 : -1;
    const next = Math.max(window.FriendAvatar.minPix,
      Math.min(window.FriendAvatar.maxPix, menu.settings().pix + step));
    if (next === menu.settings().pix) return;
    menu.setPix(next);
    applySize(next);
  }, { passive: false });

  // --- посадка -------------------------------------------------------------

  // focusWin — активное окно человека с его границами (x, y, ширина, высота).
  // Приходит от помощника через оболочку (bridge.onFocus) и живёт только в
  // странице: на сервер и в модель уезжают по-прежнему процесс и заголовок —
  // границы нужны лишь для посадки «на окне».
  let focusWin = null;

  function seatTarget() {
    const b = focusWin;
    if (!b) return null;
    if (b.w < 140 || b.h < 90) return null;     // на мелкое окно не садятся
    if (/electron/i.test(b.process)) return null; // её собственное окно
    return b;
  }

  // applySeat — «свободно», «на панель задач», «на окне» или «у края экрана».
  //
  // Почему это позиция окна, а не отдельный слой поверх рабочего стола: окно
  // компаньона и есть то, чем он виден. Панель задач системе принадлежит, и
  // «сесть на неё» — значит поставить своё окно так, чтобы её ноги (низ спрайта)
  // совпали с верхней кромкой панели. Отсюда и поза: сидя низ спрайта — это
  // то, на чём она сидит.
  function applySeat(seat) {
    return Promise.resolve(bridge.workArea()).then((area) => {
      const wa = area || { x: 0, y: 0, width: window.screen.availWidth, height: window.screen.availHeight };
      const size = windowSize();
      if (seat === 'window') {
        const t = seatTarget();
        if (!t) {
          // Активного окна с верхом нет (рабочий стол, блокировка, мелкое окно) —
          // посадка невозможна: возвращаем «свободно» и говорим почему.
          menu.setSeat('free');
          chat.showLine('her', friendLang.L('seat.noWindow'));
          return Promise.resolve();
        }
        avatar.setPose('sit');
        peek.setDir(0);
        // Ноги — на верхнюю кромку чужого окна. Ниже низа экрана и выше
        // высоты окна (окно не влезет в мир) не пускаем; по горизонтали —
        // внутри мишени, чтобы не висеть в воздухе над краем окна.
        const minFeet = wa.y + PAD_TOP + avatar.height;
        anchor.y = clamp(t.y, minFeet, wa.y + wa.height);
        const lo = Math.max(wa.x + size.w / 2, t.x + 12);
        const hi = Math.min(wa.x + wa.width - size.w / 2, t.x + t.w - 12);
        anchor.x = clamp(anchor.x, lo, Math.max(lo, hi));
      } else if (seat === 'taskbar') {
        avatar.setPose('sit');
        peek.setDir(0);
        // Её ноги (низ спрайта) — на верхней кромке панели задач. Рабочая
        // область уже не включает панель, поэтому её низ и есть эта кромка.
        anchor.y = wa.y + wa.height;
        anchor.x = clamp(anchor.x, wa.x + size.w / 2, wa.x + wa.width - size.w / 2);
      } else if (seat === 'edge-left' || seat === 'edge-right') {
        avatar.setPose('stand');
        // Окно встаёт кромкой РОВНО к краю экрана, а прячется она движением
        // спрайта внутри окна (см. «выход из-за края экрана» выше). Раньше окно
        // свешивалось за экран на четверть фигуры: видно было вертикальную полосу
        // спрайта, и это читалось как «её обрезало», а не «она выглядывает».
        peek.setDir(seat === 'edge-left' ? -1 : 1);
        winPos = {
          x: Math.round(seat === 'edge-left' ? wa.x : wa.x + wa.width - size.w),
          y: winPos.y,
        };
        anchorFromWindow(winPos);
        // Выглядывает сразу: человек только что выбрал «у края» — персонаж,
        // который после этого мгновенно исчезает, выглядит как поломка.
        peek.poke();
      } else {
        avatar.setPose('stand');
        peek.setDir(0);
      }
      const pos = windowPosForAnchor();
      return Promise.resolve(bridge.setBounds({ x: pos.x, y: pos.y, width: size.w, height: size.h }))
        .then((real) => {
          if (real && typeof real.x === 'number')      anchorFromWindow(real);
          layout();
          syncPeek();
          net.sendPosition(winPos.x, winPos.y);
        });
    }).catch((err) => {
      console.error('[friend] посадка не удалась:', err);
    });
  }

  function clamp(v, lo, hi) {
    if (hi < lo) return lo;
    return Math.max(lo, Math.min(hi, v));
  }

  // Посадка УДЕРЖАНИЕМ («удержание перед посадкой» из MateEngine): пока её
  // тащат мышью и её ноги входят в полосу верхней кромки окна-мишени (±36 px),
  // включается отсчёт. Полсекунды в полосе — случайность (протяжка «через
  // верх»), полная секунда — человек явно пристроил её к окну: тогда она
  // садится, а протяжка ПРИВЯЗЫВАЕТСЯ к новому месту (drag.rebase) — иначе
  // следующий шаг курсора вернул бы окно по старой цели, и она соскочила бы
  // с кромки. Быстрый пролёт через кромку не сажает: у него нет секунды.
  let seatDwell = 0;       // когда ноги вошли в полосу (0 = вне полосы)
  let seatAppliedAt = 0;    // момент усаживания удержанием (для onEnd)
  const SEAT_BAND = 36;      // px above/below the window top edge — полоса кромки
  const SEAT_HOLD_MS = 1000; // сколько ноги держатся на кромке

  function seatWatch() {
    if (!drag || !drag.isDragging()) { seatDwell = 0; return; }
    const t = seatTarget();
    if (!t) { seatDwell = 0; return; }
    if (menu.settings().seat === 'window') { seatDwell = 0; return; } // уже сидит
    const pos = drag.windowPos();
    if (!pos) return;
    const feetX = pos.x + PAD_X + avatar.width / 2;
    const feetY = pos.y + PAD_TOP + avatar.height;
    if (Math.abs(feetY - t.y) > SEAT_BAND || feetX < t.x - 8 || feetX > t.x + t.w + 8) {
      seatDwell = 0;
      return;
    }
    const now = Date.now();
    if (!seatDwell) { seatDwell = now; return; }
    if (now - seatDwell < SEAT_HOLD_MS) return;
    seatDwell = 0;
    seatAppliedAt = now;
    menu.setSeat('window');
    applySeat('window').then(() => {
      // Посадили посреди протяжки: окно переставлено, продолжаем тащить ОТ
      // нового места.
      if (drag.isDragging()) drag.rebase(winPos.x, winPos.y);
    });
  }
  window.addEventListener('pointermove', seatWatch, true);

  // --- реакция на клик фразой (этап 6) -------------------------------------
  //
  // Клик по ней — не только «открой поле ввода»: она отвечает короткой
  // локальной фразой. Локальной — принципиально: жест «пoked — она ответила»
  // не должен зависеть от ключа модели и сети, это реакция персонажа, а не
  // запрос в API.
  // --- озвучка её реплик (этап 7: Edge TTS) --------------------------------
  //
  // Граница приватности принципиальная: в облако Microsoft уходит ТЕКСТ её
  // реплики — то, что она произносит вслух. Экран, память и история туда не
  // попадают. Флаг доступности приходит из welcome (на сервере есть ли
  // edge-tts), переключатель «говорит вслух» — в меню; без обоих озвучки нет.
  let voiceInfo = null;
  // ttsPlaying — «в колонках сейчас её голос». Нужен фоновому слушателю wake
  // word: она не должна разбудить себя, услышав собственную озвучку из
  // динамиков микрофоном (это же эхо даёт и echoCancellation, но счётчик
  // надёжнее).
  let ttsPlaying = 0;
  // Интонация (этап 9б): её настроение и сон переводятся в сдвиги темпа
  // (проценты) и тона (герцы), сервер прогоняет их через edge-tts.
  // Сон важнее полоски: сонная — заметно медленнее и низко, даже когда
  // настроение высокое.
  function speakIntonation() {
    if (avatar.emotion() === 'sleep') return { rate: -25, pitch: -10 };
    if (moodBand === 'low') return { rate: -12, pitch: -6 };
    if (moodBand === 'high') return { rate: 12, pitch: 8 };
    return { rate: 0, pitch: 0 };
  }
  function speak(text) {
    if (!voiceInfo || !voiceInfo.available) return;
    if (menu.settings().voice === false) return;
    const t = (text || '').trim();
    if (!t) return;
    // Речь по предложениям (этап 11в): фразы с живыми паузами звучат
    // естественнее монолита, и первая фраза начинает играть, не дожидаясь
    // синтеза всей реплики.
    speakSeq(splitSentences(t));
  }

  // speakSeq играет фразы по очереди; новая реплика перебивает прошлую
  // (токен): ответ на новое сообщение не должен ждать хвоста старого.
  let speakToken = 0;
  async function speakSeq(parts) {
    const my = ++speakToken;
    const int = speakIntonation();
    for (let i = 0; i < parts.length; i++) {
      if (my !== speakToken) return;
      await speakOne(parts[i], int);
      if (my !== speakToken || i >= parts.length - 1) return;
      await new Promise((r) => setTimeout(r, 180)); // пауза между фразами — как вдох
    }
  }

  function speakOne(t, int) {
    return new Promise((resolve) => {
      const a = new Audio('/tts?text=' + encodeURIComponent(t) +
        '&rate=' + int.rate + '&pitch=' + int.pitch);
      a.addEventListener('playing', () => { ttsPlaying++; idleTouch(); });
      const off = (ev) => {
        if (ev.type === 'ended' || ev.type === 'error') {
          ttsPlaying = Math.max(0, ttsPlaying - 1);
          resolve();
        }
      };
      a.addEventListener('ended', off);
      a.addEventListener('error', off);
      a.play().catch(() => resolve()); // озвучка — украшение: не вышла, реплика в облачке
    });
  }

  // splitSentences — реплика на фразы: режем по .!?… в конце слова, длинные
  // куски (сервер озвучивает до 400 знаков) — по словам.
  function splitSentences(t) {
    const parts = [];
    let cur = '';
    for (let i = 0; i < t.length; i++) {
      cur += t[i];
      if ('.!?…'.indexOf(t[i]) >= 0 && (i + 1 >= t.length || t[i + 1] === ' ' || t[i + 1] === '"')) {
        if (cur.trim()) parts.push(cur.trim());
        cur = '';
      }
    }
    if (cur.trim()) parts.push(cur.trim());
    const out = [];
    for (const p of parts) {
      if (p.length <= 380) { out.push(p); continue; }
      let rest = p;
      while (rest.length > 380) {
        let cut = rest.lastIndexOf(' ', 380);
        if (cut < 200) cut = 380;
        out.push(rest.slice(0, cut).trim());
        rest = rest.slice(cut);
      }
      if (rest.trim()) out.push(rest.trim());
    }
    return out.length ? out : [t];
  }

  // --- голосовой ввод (этап 7: локальный faster-whisper) -------------------
  //
  // Граница приватности обратная озвучке и жёсткая: запись с микрофона
  // расшифровывается на этой машине (серверный /stt гоняет локальную модель),
  // в сеть из этой цепочки не уходит НИЧЕГО. Страница из webm-записи
  // MediaRecorder делает WAV PCM16 mono 16 кГц — тот формат, который
  // whisper-движок понимает без внешних декодеров (и без pyav на сервере).
  let sttInfo = null;

  // wavFromFloat — Float32 (-1..1) семплов 16 кГц → WAV PCM16 mono. Общий
  // код для микрофонной кнопки (/stt) и фонового слушателя имени (/wake).
  function wavFromFloat(ch) {
    const view = new DataView(new ArrayBuffer(44 + ch.length * 2));
    const str = (at, s) => { for (let i = 0; i < s.length; i++) view.setUint8(at + i, s.charCodeAt(i)); };
    str(0, 'RIFF');
    view.setUint32(4, 36 + ch.length * 2, true);
    str(8, 'WAVE');
    str(12, 'fmt ');
    view.setUint32(16, 16, true);   // длина fmt-чанка
    view.setUint16(20, 1, true);    // PCM
    view.setUint16(22, 1, true);    // mono
    view.setUint32(24, 16000, true);
    view.setUint32(28, 32000, true);
    view.setUint16(32, 2, true);
    view.setUint16(34, 16, true);   // бит на семпл
    str(36, 'data');
    view.setUint32(40, ch.length * 2, true);
    for (let i = 0; i < ch.length; i++) {
      const v = Math.max(-1, Math.min(1, ch[i]));
      view.setInt16(44 + i * 2, v < 0 ? v * 0x8000 : v * 0x7fff, true);
    }
    return new Blob([view.buffer], { type: 'audio/wav' });
  }

  function encodeWav16k(blob) {
    return (async () => {
      const raw = await blob.arrayBuffer();
      const ctx = new AudioContext();
      let audio;
      try {
        audio = await ctx.decodeAudioData(raw);
      } finally {
        ctx.close();
      }
      const RATE = 16000;
      const frames = Math.max(1, Math.ceil(audio.duration * RATE));
      const off = new OfflineAudioContext(1, frames, RATE);
      const src = off.createBufferSource();
      src.buffer = audio;             // OfflineAudioContext сам пересемплирует в 16 кГц mono
      src.connect(off.destination);
      src.start();
      const rendered = await off.startRendering();
      return wavFromFloat(rendered.getChannelData(0));
    })();
  }

  // transcribe — расшифровать запись (blob от MediaRecorder) в текст.
  // Ошибку бросает наружу: решение «что показать человеку» у того, кто зовёт.
  async function transcribe(blob) {
    const wav = await encodeWav16k(blob);
    const resp = await fetch('/stt', { method: 'POST', body: wav });
    if (!resp.ok) {
      const why = await resp.text().catch(() => '');
      throw new Error('сервер не расшифровал (статус ' + resp.status + (why ? ': ' + why.trim() : '') + ')');
    }
    const data = await resp.json();
    return (data.text || '').trim();
  }

  // --- wake word (этап 7: слышит только её имя) ----------------------------
  //
  // Как это устроено и почему приватно: страница слушает микрофон фоном, но
  // НЕ пишет и не отправляет ничего подряд. Энергетический VAD ловит только
  // речевые куски (начало по уровню, конец по 0.6 с тишины), каждый кусок
  // пересобирается в WAV 16 кГц и отправляется на /wake, где ЛОКАЛЬНЫЙ
  // faster-whisper ищет её имя. Не имя — текст выбрасывается молча (сервер
  // про него не узнаёт ничего, в лог не пишет, на диск не кладёт: временный
  // wav стирается тем же движком, что и у /stt).
  //
  // Включено только по двум разрешениям сразу: сервер подтвердил, что есть
  // чем слушать (wake в welcome), и человек сам включил «слышит её имя» в
  // меню. Пока слушает — горит зелёная точка-индикатор: микрофон открыт,
  // человек это видит.
  let wakeInfo = null;
  let wakeActive = false;
  let wakeCtx = null, wakeStream = null, wakeSrc = null, wakeProc = null;
  let wakePre = [];       // pre-roll тишины, чтобы не откусить начало слова
  let wakeSeg = [];       // кадры текущего речевого куска
  let wakeLen = 0;        // семплов в текущем куске
  let wakeSilence = 0;    // тишины в текущем куске, мс
  let wakeBusy = false;   // куск отправлен, ждём ответ — не копим следующее

  const WAKE_FRAME = 1024;          // 64 мс на 16 кГц
  const WAKE_RMS = 0.02;            // порог речи: обычный разговор у ноутбука;
                                    // ниже — шум вентилятора, а на шуме whisper
                                    // галлюцинирует словами и будит её напрасно
  const WAKE_SILENCE_MS = 600;      // тишина, закрывающая кусок
  const WAKE_PRE_FRAMES = 3;        // ~0.2 с начала слова из pre-roll
  const WAKE_MIN_SEC = 0.4;         // короче — не речь, не гоняем модель
  const WAKE_MAX_SEC = 12;          // потолок куска: длинную речь режем

  const WAKE_PHRASES = () => friendLang.list('wake.phrases');
  let wakePhraseIndex = 0;

  function wakeEnabled() {
    return !!(wakeInfo && wakeInfo.available) && menu.settings().wake === true;
  }

  // Эхо-защита: её собственный голос из динамиков не должен будить её же
  // (speak считаетPlaying); во время записи микрофонной кнопкой слушатель
  // тоже уступает место (одна запись на два потребителя не нужна).
  function wakeGuard() {
    return ttsPlaying > 0 || (window.friendChat && window.friendChat.micRecording());
  }

  function wakeDotShow(on) {
    wakeDot.hidden = !on;
  }

  async function startWakeLoop() {
    if (wakeActive) return;
    try {
      wakeStream = await navigator.mediaDevices.getUserMedia({
        audio: { echoCancellation: true, noiseSuppression: true, autoGainControl: false },
      });
    } catch (e) {
      wakeInfo = null; // микрофона нет/отказ — слушатель честной молчит
      return;
    }
    wakeCtx = new AudioContext({ sampleRate: 16000 });
    wakeSrc = wakeCtx.createMediaStreamSource(wakeStream);
    wakeProc = wakeCtx.createScriptProcessor(WAKE_FRAME, 1, 1);
    wakeProc.onaudioprocess = wakeAudio;
    wakeSrc.connect(wakeProc);
    wakeProc.connect(wakeCtx.destination); // выход процессора молчит — это «потребитель» для запуска
    wakeActive = true;
    wakeDotShow(true);
  }

  function stopWakeLoop() {
    wakeActive = false;
    wakeDotShow(false);
    if (wakeProc) { wakeProc.onaudioprocess = null; try { wakeProc.disconnect(); } catch (e) {} wakeProc = null; }
    if (wakeSrc) { try { wakeSrc.disconnect(); } catch (e) {} wakeSrc = null; }
    if (wakeCtx) { wakeCtx.close(); wakeCtx = null; }
    if (wakeStream) { wakeStream.getTracks().forEach((t) => t.stop()); wakeStream = null; }
    wakePre = []; wakeSeg = []; wakeLen = 0; wakeSilence = 0; wakeBusy = false;
  }

  function wakeAudio(ev) {
    if (wakeBusy) return;
    if (wakeGuard()) { wakeSeg = []; wakeLen = 0; wakeSilence = 0; return; }
    const input = ev.inputBuffer.getChannelData(0);
    let sum = 0;
    for (let i = 0; i < input.length; i++) sum += input[i] * input[i];
    const rms = Math.sqrt(sum / input.length);
    const frame = Float32Array.prototype.slice.call(input);
    if (rms > WAKE_RMS) {
      if (!wakeSeg.length) wakePre.slice(-WAKE_PRE_FRAMES).forEach((f) => wakeSeg.push(f));
      wakeSeg.push(frame);
      wakeLen += input.length;
      wakeSilence = 0;
      if (wakeLen >= WAKE_MAX_SEC * 16000) finishWakeSegment();
    } else if (wakeSeg.length) {
      wakeSilence += (WAKE_FRAME * 1000) / 16000;
      if (wakeSilence >= WAKE_SILENCE_MS) finishWakeSegment();
    } else {
      wakePre.push(frame);
      if (wakePre.length > WAKE_PRE_FRAMES) wakePre.shift();
    }
  }

  async function finishWakeSegment() {
    const seg = wakeSeg;
    wakeSeg = []; wakeLen = 0; wakeSilence = 0;
    let n = 0;
    for (const f of seg) n += f.length;
    if (n < WAKE_MIN_SEC * 16000) return;
    const out = new Float32Array(n);
    let o = 0;
    for (const f of seg) { out.set(f, o); o += f.length; }
    wakeBusy = true;
    try {
      await fetch('/wake', { method: 'POST', body: wavFromFloat(out) });
      // Ответ не разбираем: реакция (wake) приходит кадром WakeMsg по WS —
      // так же, как все её реплики.
    } catch (e) {
      // сервер не ответил — кусок потерян, слушатель продолжает жить
    } finally {
      wakeBusy = false;
    }
  }

  // wakeTick — включение/выключение слушателя по двум разрешениям. Проверка
  // раз в две секунды, не по событию: переключатель в меню человек щёлкает
  // в любой момент, и слушатель обязан подстроиться без перезагрузки.
  function wakeTick() {
    if (wakeEnabled() && !wakeActive) startWakeLoop();
    else if (!wakeEnabled() && wakeActive) stopWakeLoop();
  }
  setInterval(wakeTick, 2000);
  wakeTick();

  // Ответ на имя: будим (если спит), оживляем, машем, короткая фраза.
  // Сказанные ей слова сервер уже поставил в разговорную очередь — её
  // ответ на них приедет отдельным ReplyMsg (и будет озвучен, как обычно).
  let wakeEvents = 0;
  net.on('wake', () => {
    wakeEvents++;
    avatar.setEmotion('happy');
    if (avatar.wave) avatar.wave();
    const phrases = WAKE_PHRASES();
    const phrase = phrases[wakePhraseIndex++ % phrases.length];
    chat.showLine('her', phrase);
    speak(phrase);
  });

  const POKE_PHRASES = () => friendLang.list('poke.phrases');
  let lastPoke = 0;
  let lastPokeIndex = -1;

  function pokeSpeak() {
    // Спящую клик будит: «спит и не реагирует» — то же состояние, что
    // «её нет». Фраза пробуждения своя, не из пула.
    if (avatar.emotion() === 'sleep') {
      avatar.setEmotion(avatar.calmFace());
      const wake = friendLang.L('wake.awakePhrase');
      chat.showLine('her', wake);
      avatar.say(wake);
      speak(wake);
      lastPoke = Date.now();
      return;
    }
    const now = Date.now();
    // Короткий «тык-тык-тык» не должен превращать её в автомат: ответ один
    // на десять секунд, дальше — молча открывает поле ввода.
    if (now - lastPoke < 10000) return;
    lastPoke = now;
    const phrases = POKE_PHRASES();
    let idx = Math.floor(Math.random() * phrases.length);
    if (idx === lastPokeIndex) idx = (idx + 1) % phrases.length;
    lastPokeIndex = idx;
    const phrase = phrases[idx];
    chat.showLine('her', phrase);
    avatar.say(phrase);
    speak(phrase);
  }

  // --- прогулка (этап 6: «ходит по экрану») --------------------------------
  //
  // Идёт окном: персонаж живёт окном, и «ходить» для него — значит ехать
  // окном по столу. Поэтому шаг = мелкий шаг окна (bridge.setBounds), а
  // кадры ног даёт аватар (setWalk). Скорость намеренно пешеходная: окно,
  // летящее 200 px/с, — это «её понесли», а не прогулка.
  //
  // У края рабочей области она разворачивается: тот же лист шага, зеркальный
  // по пикселям (setWalk(-1)), и лицо (взгляд) туда же.

  let walk = null;   // { dir, until, speed }

  function walkTick() {
    if (!walk) return;
    if (Date.now() >= walk.until) { stopWalk('прогулка дошла до конца'); return; }
    if (bridge.isNative) {
      // Пока идёт меню, окно шире персонажа: ехать на его ширине нельзя,
      // персонаж уедет от окна. Прогулка ждёт, пока меню закроют.
      if (menuBounds) return;
      Promise.resolve(bridge.workArea()).then((area) => {
        const wa = area || { x: 0, y: 0, width: window.screen.availWidth, height: window.screen.availHeight };
        const size = windowSize();
        let x = winPos.x + walk.dir * walk.speed;
        x = Math.round(clamp(x, wa.x, wa.x + Math.max(0, wa.width - size.w)));
        const pos = { x: x, y: winPos.y };
        return Promise.resolve(bridge.setBounds({ x: pos.x, y: pos.y, width: size.w, height: size.h }))
          .then((real) => {
            if (real && typeof real.x === 'number') { anchorFromWindow(real); layout(); }
            // Дошла до края — разворот: лист шага и взгляд в другую сторону.
            // Дошла до края рабочей области — разворот: тот же лист шага,
            // зеркальный, и взгляд в другую сторону.
            if (x <= wa.x && walk && walk.dir < 0) startWalk(1, walk.until);
            else if (x + size.w >= wa.x + wa.width && walk && walk.dir > 0) startWalk(-1, walk.until);
          });
      }).catch((err) => console.error('[friend] прогулка не едет:', err));
    }
  }

  function startWalk(dir, until) {
    const d = dir > 0 ? 1 : dir < 0 ? -1 : 1;
    const keepTimer = walk && walk.timer;
    if (keepTimer) clearInterval(keepTimer);
    walk = { dir: d, until: until || (Date.now() + 12000), speed: 2.4, timer: null };
    avatar.setWalk(d);
    // Взгляд едет туда же, куда она шагает: смотреть на курсор, шагая влево,
    // — это «идёт не глядя».
    avatar.lookAt(d * 40, 8);
    walk.timer = setInterval(walkTick, 80);
  }

  function stopWalk(reason) {
    if (!walk) return;
    const t = walk.timer;
    walk = null;
    if (t) clearInterval(t);
    avatar.setWalk(0);
    console.log('[friend] прогулка кончилась:', reason);
    net.sendPosition(winPos.x, winPos.y);   // запомнить, где она встала
  }

  // Длинный проход по экрану (ходьба по экрану из Desktop Mate): не «шаг туда-
  // сюда на десять секунд», а переход через весь рабочий стол: до ближайшего
  // КРАЙНЕГО края, разворот там, полный переход к другому краю и остановка.
  // scale — только для самопроверки: умножает скорость, чтобы километровый
  // маршрут проходил за секунды, а не за минуту.
  function startRouteWalk(scale) {
    const k = Math.max(1, Math.min(8, scale || 1));
    return Promise.resolve(bridge.workArea()).then((area) => {
      const wa = area || { x: 0, width: window.screen.availWidth };
      const size = windowSize();
      const center = winPos.x + size.w / 2;
      const mid = wa.x + wa.width / 2;
      // Идём к ДАЛЬНЕМУ краю: маршрут длиннее и видно весь экран.
      const d = center < mid ? 1 : -1;
      const farEdge = d > 0 ? wa.x + wa.width - size.w : wa.x;
      const leg1 = Math.abs(farEdge - winPos.x);
      const leg2 = Math.max(0, wa.width - size.w);
      const speed = 2.4 * k;                 // пикселей за тик (80 мс)
      const durMs = ((leg1 + leg2) / speed) * 80 + 3000;
      startWalk(d, Date.now() + durMs);
      walk.speed = speed;
    });
  }

  // goHome — вернуть её в привычный угол: единственное действие, которое всегда
  // вытаскивает персонажа из любой ситуации «я его не вижу».
  function goHome() {
    return Promise.resolve(bridge.home()).then((h) => {
      avatar.setPose('stand');
      return menu.settings().seat === 'free' ? h : h;
    }).then((h) => {
      const size = windowSize();
      return bridge.setBounds({ x: h.x, y: h.y, width: size.w, height: size.h });
    }).then((real) => {
      if (real && typeof real.x === 'number') anchorFromWindow(real);
      layout();
      net.sendPosition(winPos.x, winPos.y);
    });
  }

  // --- меню: расширение окна -----------------------------------------------

  // Меню не помещается в окно шириной с персонажа, поэтому на время его
  // открытия окно расширяется в сторону, где есть место на экране, а после
  // закрытия возвращается к размеру персонажа. Расширяем ВБОК и на месте: если
  // растить окно «во все стороны», персонаж прыгнет в новую середину.
  let menuBounds = null;

  function growForMenu(open) {
    if (!bridge.isNative || !bridge.setBounds) return Promise.resolve();
    if (!open) {
      if (!menuBounds) return Promise.resolve();
      const back = menuBounds;
      menuBounds = null;
      document.body.classList.remove('menu-left', 'menu-right');
      // Размер возвращаем, а МЕСТО берём текущее: пока меню было открыто, её
      // могли перетащить. Старое место вернуло бы её назад без спроса.
      return Promise.resolve(bridge.getPosition()).then((cur) => (
        bridge.setBounds({ x: cur.x, y: cur.y, width: back.width, height: back.height })
      )).then((real) => {
        if (real && typeof real.x === 'number') anchorFromWindow(real);
        layout();
      });
    }
    if (menuBounds) return Promise.resolve();
    const size = windowSize();
    return Promise.resolve(bridge.workArea()).then((area) => {
      const wa = area || { x: 0, y: 0, width: window.screen.availWidth, height: window.screen.availHeight };
      const left = winPos.x - wa.x;
      const right = (wa.x + wa.width) - (winPos.x + size.w);
      // Слева места больше — растем влево (так меню не упирается в край экрана).
      const toLeft = left >= MENU_W || left >= right;
      menuBounds = { x: winPos.x, y: winPos.y, width: size.w, height: size.h };
      const next = {
        x: toLeft ? winPos.x - MENU_W : winPos.x,
        y: winPos.y,
        width: size.w + MENU_W,
        height: size.h,
      };
      document.body.classList.add(toLeft ? 'menu-left' : 'menu-right');
      return Promise.resolve(bridge.setBounds(next)).then((real) => {
        if (real && typeof real.x === 'number') anchorFromWindow(real);
        layout();
      });
    });
  }

  // Правая кнопка по персонажу — меню. Именно по САМОМУ персонажу: окно шире его,
  // и меню по правому клику в пустое место отнимало бы клик у рабочего стола.
  window.addEventListener('contextmenu', (ev) => {
    if (!isOverAvatar(ev.clientX, ev.clientY)) return;
    ev.preventDefault();
    idleTouch();
    menu.toggle();
  });

  // --- связь и состояние ---------------------------------------------------

  // Понимание окна человека: оболочка присылает события от помощника focuswatch
  // («активное окно сменилось»). Страница пересылает их серверу как есть —
  // решать, что это «редактор», «браузер» или «игра», должен сервер
  // (server/internal/focus), где из этих данных собирается промпт.
  //
  // Последнее окно запоминается: если канал лёг, сервер свой контекст теряет,
  // поэтому при восстановлении связи активное окно отправляется заново
  // (та же логика, что с позицией ниже).
  // Прогулка (см. ниже): человек рядом = мышь шевелится. lastSeen обновляется
  // на кадрах focus с маленьким idle (idle- heartbeat помощника приходит
  // каждые 30 с, поэтому «рядом» обновляется регулярно).
  let lastSeen = Date.now();
  let walkChecked = 0;
  let lastFocus = null;
  bridge.onFocus((d) => {
    if (!d || !d.process) return;
    lastFocus = d;
    idle.setOsIdle((d.idle || 0) * 1000);    // простой для «сон/уход»
    if (d.idle < 2) lastSeen = Date.now();   // он шевелил мышь меньше двух минут назад
    // Границы активного окна (посадка «на окне»). Границы живут ТОЛЬКО здесь:
    // на сервер и в модель они не отправляются — там по-прежнему только
    // процесс и заголовок. Нет границ (или новое окно без геометрии) —
    // сажать не на что, мишень снимается.
    const b = d.bounds;
    focusWin = (Array.isArray(b) && b.length === 4 && b.every((n) => typeof n === 'number' && isFinite(n)))
      ? { process: d.process, x: b[0], y: b[1], w: b[2], h: b[3] }
      : null;
    // Сидит «на окне» — следует за активным окном: сменилось приложение,
    // она переезжает на его верх. Пока её держат мышью или открыто меню —
    // не трогаем: человек решает сам.
    if (focusWin && menu.settings().seat === 'window' && !drag.isDragging() && !menu.isOpen()) {
      applySeat('window');
    }
    net.send({ type: 'focus', app: d.process, title: d.title || '', idle: d.idle || 0 });
  });
  function walkWatch() {
    const now = Date.now();
    if (walk) { walkChecked = now; return; }
    // Рядом и молчит: каждые 5 минут chance. Не «ходит всегда»: прогулка —
    // привычка, а не конвейер.
    if (now - walkChecked < 5 * 60 * 1000) return;
    walkChecked = now;
    if (!bridge.isNative) return;
    const seat = menu.settings().seat;
    if (seat !== 'free') return;                 // на панели и у края не шагают
    if (menu.isOpen() || chat.isOpen()) return;  // с меню и в разговоре не шагают
    if (drag && drag.isDragging()) return;       // её держат
    if (avatar.emotion() === 'sleep') return;    // спит, а не гуляет
    const quiet = now - lastSeen;
    if (quiet < 5 * 60 * 1000 || quiet > 60 * 60 * 1000) return;
    if (Math.random() > 0.3) return;
    // Рядом и молчит уже 5–10 минут (дальше — засыпание idlewatch'а):
    // четверть случаев — длинный проход через весь экран, не «туда-сюда».
    if (quiet < 10 * 60 * 1000 && Math.random() < 0.25) {
      startRouteWalk(1);
      return;
    }
    startWalk(Math.random() < 0.5 ? -1 : 1, Date.now() + 8000 + Math.random() * 8000);
  }

  // --- настроение (этап 5) -------------------------------------------------
  // applyMood — применить состояние к персонажу: полоска настроения решает
  // «лицо покоя» (low — грустинка, high — лучезарная, mid — спокойная).
  // Числа (энергия/привязанность) до лица не доходят: их слышно в речи,
  // а на лице — только настроение.
  function applyMood(m) {
    if (!m || typeof m.mood !== 'number') return;
    const band = m.band || (m.mood < 0.35 ? 'low' : m.mood > 0.65 ? 'high' : 'mid');
    moodBand = band;
    avatar.setMoodBand(band);
  }
  // Полоска настроения на странице — для интонации озвучки (этап 9б).
  let moodBand = 'mid';
  // Кадр state с настроением: сервер присылает его, когда настроение заметно
  // сдвинулось (сообщение человека подняло, игнор опустил, ночь размое).
  // Единственный обработчик 'state' на странице — этот; позиция приходит
  // отдельным подтверждением и обрабатывается в drag.js.
  net.on('state', (msg) => {
    if (msg && msg.mood) applyMood(msg.mood);
  });

  net.on('status', (s) => {
    el.dot.className = 'dot' + (s === 'on' ? ' on' : ' off');
    // Связь вернулась — активное окно тоже уезжает заново: за время простоя
    // сервер свой контекст теряет, а помощник новых смен не присылает,
    // пока окно не поменяется.
    if (s === 'on' && lastFocus) {
      net.send({ type: 'focus', app: lastFocus.process, title: lastFocus.title || '', idle: lastFocus.idle || 0 });
    }
    // Связь вернулась — отдаём серверу актуальную позицию: за время простоя
    // окно могли подвинуть, и сервер об этом не знает.
    //
    // Но только НАСТОЯЩЕЕ окно (оболочка). В браузере позиция окна — это
    // выдуманная величина: у страницы нет окна, и она считает «домашнюю точку»
    // от размера своей панели. Пока эта проверка отсутствовала, открытая в
    // браузере страница при каждом переподключении молча перезаписывала
    // сохранённую позицию персонажа — то есть персонаж переезжал сам.
    if (s === 'on' && bridge.isNative) {
      bridge.getPosition().then((p) => {
        anchorFromWindow(p);
        layout();
        net.sendPosition(p.x, p.y);
      });
    }
  });

  let appliedOnce = false;
  net.on('welcome', (msg) => {
    const av = msg.avatar || {};
    // Язык персоны — поле конфига (avatar.language): страница собрана на
    // русском по умолчанию, здесь переключаем словарь и перекрашиваем
    // подписи, собранные один раз (меню, поле ввода, каркас).
    if (av.language && av.language !== friendLang.code()) {
      friendLang.set(av.language);
      applyLang();
      if (chat.setLanguage) chat.setLanguage();
      if (menu.setLanguage) menu.setLanguage();
    }
    if (av.name) {
      personaName = av.name;
      el.name.textContent = av.name;
      menu.setName(av.name);
      applyLang();
    }
    chat.setName(av.name);
    // История разговора: сервер помнит последние реплики, пока жив, — после
    // перезагрузки страницы разговор не выглядит начатым с чистого листа.
    chat.showHistory(msg.history);

    // Настроение из состояния сервера (этап 5): лицо покоя соответствует
    // тому, что она пережила, а не «дежурному спокойствию» после перезагрузки.
    applyMood(msg.state && msg.state.mood);

    // Озвучка (этап 7): сервер сообщает, есть ли голос (edge-tts) и какой.
    voiceInfo = msg.voice || null;

    // Голосовой ввод (этап 7): по stt из welcome показываем кнопку микрофона.
    sttInfo = msg.stt || null;
    chat.setVoiceInput(!!(sttInfo && sttInfo.available));

    // Wake word (этап 7): сервер заявил слушание её имени — фоновый
    // слушатель стартует, когда человек включит «слышит её имя» в меню
    // (wakeTick проверяет оба разрешения).
    wakeInfo = msg.wake || null;

    // Настройки из прошлого запуска (размер, посадка, переключатели).
    const saved = menu.settings();
    avatar.setScale(saved.pix);
    applyFollow(saved.follow);
    if (saved.dance) applyDance(true);
    applyKeepTop(saved.keepTop);
    // Свой размер окна сообщаем ПОСЛЕ применения масштаба и именно СВОЙ —
    // спрайт плюс его поля, — а не число из конфига сервера.
    //
    // Пока здесь стоял размер из конфига (320×420), каждое переподключение к
    // серверу переписывало настоящий размер окна: окно сжималось меньше
    // персонажа, и от неё оставалась одна голова ("тела не видно"), меню уезжало
    // от неё, а «выход» из-за края считался по чужой геометрии. Хуже всего было
    // со сторожем размера: он каждые три секунды возвращал окно к этому числу,
    // поэтому картинка дёргалась.
    if (bridge.setSize) {
      const own = windowSize();
      bridge.setSize(own.w, own.h);
    }

    const pos = (msg.state && msg.state.position) || { x: 0, y: 0 };
    if (appliedOnce) return;
    appliedOnce = true;

    const place = pos.x !== 0 || pos.y !== 0
      // Позиция уже сохранена — окно возвращается туда, где его оставили. Если
      // оболочка поставит его в другое место, она сама об этом сообщит
      // (bridge.onPositionCorrected выше), и сервер узнает настоящее место.
      ? () => bridge.setPosition(pos.x, pos.y)
      // Первый запуск: домашняя точка — правый нижний угол экрана. Мост знает её
      // и в браузере, и в оболочке (см. bridge.js), поэтому логика одна.
      : () => Promise.resolve(bridge.home()).then((h) => bridge.setPosition(h.x, h.y));

    // Порядок важен: сначала МЕСТО, потом РАЗМЕР — размер ставится по месту,
    // которое только что назвала оболочка. Иначе окно шириной с персонажа
    // оказалось бы в левом верхнем углу вместо сохранённого места.
    const size = windowSize();
    Promise.resolve(place())
      .then((real) => {
        const at = real && typeof real.x === 'number' ? real : null;
        if (at) { anchorFromWindow(at); layout(); }
        return bridge.setBounds({
          x: at ? at.x : winPos.x,
          y: at ? at.y : winPos.y,
          width: size.w,
          height: size.h,
        });
      })
      .then((real) => {
        if (real && typeof real.x === 'number') anchorFromWindow(real);
        layout();
        if (pos.x === 0 && pos.y === 0) net.sendPosition(winPos.x, winPos.y);
        // Посадка применяется ПОСЛЕ позиции: иначе сохранённая позиция вернула бы
        // её с панели задач на старое место.
        if (saved.seat && saved.seat !== 'free') applySeat(saved.seat);
        document.body.classList.add('loaded');
      })
      .catch((err) => {
        // Ошибка оболочки не должна оставлять окно пустым: показываем персонажа
        // там, где он есть, и говорим об этом в консоль.
        console.error('[friend] не удалось поставить окно:', err);
        document.body.classList.add('loaded');
      });
  });

  // --- кадры и пульс -------------------------------------------------------

  // Кадры анимации: update получает реальную дельту времени, и он же — пульс для
  // оболочки. Пульс стоит ИМЕННО здесь, а не на таймере: таймер продолжает
  // тикать, даже когда на экране давно ничего не рисуется, и сторож молчал бы
  // ровно в том случае, для которого он нужен.
  app.ticker.add(() => {
    avatar.update(app.ticker.deltaMS);
    tickPeek(app.ticker.deltaMS);
    walkWatch();
    if (bridge.beat) bridge.beat();
  });

  // Прогулку ведёт отдельный интервал, а НЕ тикер Pixi: когда персонажа
  // перекрывают другим окном и кадры прекращаются, её «понесли» бы через
  // стол без ног (setWalk живёт на таймере, кадры — в тикере).

  net.connect();

  window.friendApp = {
    app: app,
    avatar: avatar,
    net: net,
    bridge: bridge,
    drag: drag,
    pet: pet,
    chat: chat,
    menu: menu,
    peek: peek,
    // music — «реакция на звук» (js/music.js): самопроверка гоняет
    // детектор тестовыми уровнями (feed-режим) и смотрит на танец.
    music: music,
    // idle — «сон/уход после простоя» (js/idlewatch.js): самопроверка
    // задаёт простой числом (feed) и следит за фазой awake/asleep/gone.
    idle: idle,
    // walkRoute(scale) — «ходьба по экрану»: длинный проход до дальнего
    // края и полный переход к другому. scale — ускорение для самопроверки.
    walkRoute: startRouteWalk,
    stopWalk: stopWalk,
    // applyPeek — пересчитать смещение «выхода» прямо сейчас, не дожидаясь
    // следующего кадра. Нужно проверке оболочки: она меряет обе крайние позы, и
    // ждать кадр между переключением и замером — это гонка с анимацией.
    applyPeek: syncPeek,
    peekOffset: function () { return peekDx; },
    layout: layout,
    applySize: applySize,
    applySeat: applySeat,
    goHome: goHome,
    // applyMood — применить полоску настроения (этап 5). Открытая функция:
    // самопроверка оболочки проверяет ей связь «настроение → лицо», не
    // дожидаясь суток дрейфа.
    applyMood: applyMood,
    // startWalk/stopWalk/walking — прогулка (этап 6) под управлением
    // самопроверки: она меряет, что окно ЕДЕТ, пока идут кадры шага.
    startWalk: startWalk,
    stopWalk: stopWalk,
    walking: () => avatar.walking(),
    // voiceInfo — доступность озвучки из welcome (этап 7): открытая проверка
    // для самопроверки оболочки (голос есть/нет, имя голоса).
    voiceInfo: () => voiceInfo,
    // sttInfo + transcribe — голосовой ввод (этап 7): флаг из welcome и та же
    // расшифровка, которой пользуется микрофонная кнопка. Самопроверка гоняет
    // по ней полную цепочку: mp3 от Edge TTS → WAV 16 кГц → /stt → текст.
    sttInfo: () => sttInfo,
    transcribe: transcribe,
    // Wake word (этап 7): состояние слушателя для самопроверки, её прямой
    // запуск/остановка, отправка сегмента на /wake и её фразы ответа на имя.
    wakeState: () => ({
      available: !!(wakeInfo && wakeInfo.available),
      enabled: wakeEnabled(),
      on: wakeActive,
    }),
    wakeStart: () => startWakeLoop(),
    wakeStop: () => stopWakeLoop(),
    wakeSend: (blob) => encodeWav16k(blob).then(
      (wav) => fetch('/wake', { method: 'POST', body: wav }).then((r) => r.json()),
    ),
    wakePhrases: () => WAKE_PHRASES().slice(),
    wakeEvents: () => wakeEvents,
    windowSize: windowSize,
    anchor: function () { return { x: anchor.x, y: anchor.y }; },
    winPos: function () { return { x: winPos.x, y: winPos.y }; },
    // focusWin — окно-мишень посадки (самопроверка посадки «на окне»):
    // процесс + границы последнего активного окна, либо null.
    focusWin: function () { return focusWin ? Object.assign({}, focusWin) : null; },
    // anchorFromWindow — «окно стоит здесь, пересчитай её ноги»: сверка
    // счётчиков страницы с фактическим местом окна.
    anchorFromWindow: anchorFromWindow,
    pads: { x: PAD_X, top: PAD_TOP, bottom: PAD_BOTTOM, menu: MENU_W },
    // loopState — ответ сторожу оболочки на вопрос «цикл отрисовки жив?»
    // (см. desktop/main.js, checkDrawing): «рисует» — кадров нет по вине системы
    // (окно перекрыто, экран погашен), «остановлен» — поломка. Отвечаем ИМЕННО
    // состоянием тикера, а не словом: по нему оболочка решает, перезагружать
    // страницу или нет.
    loopState: () => (app.ticker.started ? 'рисует' : 'остановлен'),
  };
  // Метка для самопроверки оболочки: по ней видно, что страница пережила
  // перезагрузку после «исчезновения» (desktop/main.js, часть проверки 6).
  window.friendAlive = true;
})();
