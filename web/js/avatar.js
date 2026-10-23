// avatar.js — сама Мира: пиксельная аниме-девушка.
//
// Почему рисуем кодом, а не кладём готовый спрайт: готовый арт — это чужие
// лицензии и чужая сетка кадров, а нам нужны СВОИ состояния (моргание,
// разговор, взгляд за курсором, взмах рукой, эмоции). Здесь персонаж собран из
// пиксельной сетки 40×56 с одной палитрой и автоматической обводкой: тело —
// один рисунок, лицо и руки — отдельные части, поэтому их можно анимировать по
// отдельности, не перерисовывая всё.
//
// Как устроено:
//   • grid() — сетка пикселей, в которую рисуют rect/ell/line;
//   • outline() — обводит силуэт тёмным. Именно обводка превращает набор фигур в
//     спрайт, а не в цветное пятно;
//   • paint() — сетка → прямоугольники PixiJS (один прямоугольник на ряд
//     одинаковых пикселей, а не на пиксель: иначе это тысячи объектов кадра);
//   • PIX — размер пикселя. Он ЦЕЛЫЙ: дробный масштаб размыл бы пиксельность.
window.FriendAvatar = (function () {
  'use strict';

  // Размер сетки спрайта. А вот размер ОДНОГО пикселя спрайта — не константа: он
  // приходит в create() (opts.pix) и меняется ползунком «размер» в меню.
  //
  // Почему именно целым числом: пиксель — это квадрат одного цвета. Дробный
  // масштаб превратил бы его в размытое пятно с полутонами на краях, и вся
  // пиксельность исчезла бы. Поэтому «размер» — это 2..12 пикселей на клетку, и
  // шаг ровно один пиксель.
  const GW = 40;               // ширина сетки в пикселях спрайта
  const GH = 56;               // высота
  const PIX = 5;               // внутренний размер клетки: 40×56 → спрайт 200×280
  // Размер клетки НА ЭКРАНЕ (ползунок «размер»): 2..12 пикселей. Меняется не
  // перерисовкой сетки, а масштабом готового спрайта (setScale) — шаг берётся
  // так, чтобы клетка оставалась целым числом пикселей, иначе края квадратов
  // размываются и пиксельность пропадает.
  const MIN_PIX = 2;
  const MAX_PIX = 12;
  const DEFAULT_PIX = 5;
  const SPRITE_W = GW * PIX;   // 200
  const SPRITE_H = GH * PIX;   // 280

  // Палитра. Силуэт должен читаться издалека: волосы светлее кожи, юбка темнее
  // блузы, обводка тёмно-фиолетовая (чёрная выглядит грубо).
  const C = {
    outline: 0x232a44,
    // Голубые волосы. Глаза при этом тёплые (золотые): два одинаковых синих
    // пятна рядом читались бы как одно лицо-клякса, а контраст глаз и волос
    // держит взгляд.
    hairBack: 0x3f63c8,
    hairBase: 0x6c95ff,
    hairLight: 0xa9c9ff,
    skin: 0xffe2d2,
    skinShade: 0xf2c2ae,
    blush: 0xff9fb6,
    blouse: 0xfbfcff,
    blouseShade: 0xdfe4f2,
    skirt: 0x3b4670,
    skirtShade: 0x2c3555,
    ribbon: 0xe8455f,
    ribbonShade: 0xc4384f,
    sock: 0xfdfdfd,
    shoe: 0x40404f,
    heart: 0xff7aa8,
    heartDark: 0xd94f7e,
    // Буква «Z» для сонных частиц: голубовато-фиолетовая, в тон волос — и
    // видна на любом фоне рабочего стола (тёплые тона сливались бы с кожей).
    zzz: 0x9db4ff,
    zzzDark: 0x5d76c8,
    eyeWhite: 0xffffff,
    iris: 0xffc061,
    irisDark: 0xe0822f,
    pupil: 0x1d2233,
    lash: 0x3a2a3f,
    mouth: 0xa8536a,
  };

  const EMOTIONS = ['calm', 'happy', 'sad', 'think', 'sleep'];

  // Разметка лица и тела в пикселях сетки. Держим её отдельными числами: когда
  // глаза «уезжали» вместе с чёлкой, причину было видно только здесь.
  const LAYOUT = {
    faceTop: 12,   // откуда начинается открытая кожа (ниже чёлки)
    eyeY: 12,      // верхний ряд глаз
    eyeLX: 12,     // левый глаз
    eyeRX: 21,     // правый глаз
    browY: 11,
    mouthY: 21,
    chin: 25,
  };

  // --- сетка и примитивы ---------------------------------------------------

  function grid(w, h) {
    return { w: w, h: h, px: new Array(w * h).fill(null) };
  }

  function set(g, x, y, c) {
    x = Math.round(x);
    y = Math.round(y);
    if (x >= 0 && y >= 0 && x < g.w && y < g.h) g.px[y * g.w + x] = c;
  }

  function rect(g, x, y, w, h, c) {
    for (let j = 0; j < h; j++) {
      for (let i = 0; i < w; i++) set(g, x + i, y + j, c);
    }
  }

  function ell(g, cx, cy, rx, ry, c) {
    for (let y = Math.floor(cy - ry); y <= Math.ceil(cy + ry); y++) {
      for (let x = Math.floor(cx - rx); x <= Math.ceil(cx + rx); x++) {
        const dx = (x - cx) / rx;
        const dy = (y - cy) / ry;
        if (dx * dx + dy * dy <= 1) set(g, x, y, c);
      }
    }
  }

  function line(g, x0, y0, x1, y1, c) {
    const steps = Math.max(Math.abs(x1 - x0), Math.abs(y1 - y0));
    for (let i = 0; i <= steps; i++) {
      const t = steps === 0 ? 0 : i / steps;
      set(g, x0 + (x1 - x0) * t, y0 + (y1 - y0) * t, c);
    }
  }

  // outline — тёмная кромка по границе с прозрачным: силуэт перестаёт быть
  // набором фигур.
  function outline(g, c) {
    const src = g.px.slice();
    const at = (x, y) => (x < 0 || y < 0 || x >= g.w || y >= g.h ? null : src[y * g.w + x]);
    for (let y = 0; y < g.h; y++) {
      for (let x = 0; x < g.w; x++) {
        if (src[y * g.w + x] === null) continue;
        if (at(x - 1, y) === null || at(x + 1, y) === null || at(x, y - 1) === null || at(x, y + 1) === null) {
          g.px[y * g.w + x] = c;
        }
      }
    }
  }

  // flipX — зеркальная копия: правая половина рисуется как левая.
  function flipX(g) {
    const out = grid(g.w, g.h);
    for (let y = 0; y < g.h; y++) {
      for (let x = 0; x < g.w; x++) out.px[y * g.w + x] = g.px[y * g.w + (g.w - 1 - x)];
    }
    return out;
  }

  // paint — сетка → прямоугольники PixiJS (один прямоугольник на ряд
  // одинаковых пикселей, а не на пиксель: иначе это тысячи объектов кадра).
  // Пиксель с альфой (setA) рисовался раньше фоном: на теле он выглядел как
  // вырезанное окошко. Теперь альфа пишется поверх: fillAlpha делает её тенью.
  function paint(PIXI, g, px) {
    const gfx = new PIXI.Graphics();
    // Проход по сплошным пикселям (run-полосами).
    for (let y = 0; y < g.h; y++) {
      let x = 0;
      while (x < g.w) {
        const c = g.px[y * g.w + x];
        if (c === null || typeof c === 'object') { x++; continue; }
        let run = 1;
        while (x + run < g.w && g.px[y * g.w + x + run] === c) run++;
        gfx.beginFill(c, 1);
        gfx.drawRect(x * px, y * px, run * px, px);
        gfx.endFill();
        x += run;
      }
    }
    // Полупрозрачные пиксели — вторым проходом поверх.
    for (let y = 0; y < g.h; y++) {
      for (let x = 0; x < g.w; x++) {
        const c = g.px[y * g.w + x];
        if (c === null || typeof c !== 'object') continue;
        gfx.beginFill(c.c, c.a);
        gfx.drawRect(x * px, y * px, px, px);
        gfx.endFill();
      }
    }
    return gfx;
  }

  // --- части спрайта -------------------------------------------------------

  // Тело: волосы сзади, ноги, юбка, блуза, шея, голова, чёлка, лента, ахоге.
  // Лицо и руки сюда не входят — ими управляют отдельно.
  //
  // pose меняет ТОЛЬКО низ тела: стоя (stand) и сидя (sit). Голова, лицо и руки
  // остаются там же, поэтому эмоции, взгляд и разговор работают в обеих позах без
  // второй копии кода.
  // Шагающие ноги. Кадр = смещение каждой ноги из покоя: передняя уходит в
  // сторону движения (дальше всех, на всю длину ноги), ведомая отстаёт назад.
  // Ось X кадра ориентирована на ДВИЖЕНИЕ (вперёд = направо), поэтому для
  // ходьбы налево кадр просто зеркалится — разворот не требует второго листа.
  // Ведомая нога рисуется полупрозрачной (setA): как в тени собственного тела.
  function legGrids() {
    // Ноги в покое рисуются на своих местах: левая x17 (тень), правая x21.
    // Для кадра x каждой ноги = место покоя + смещение.
    function mk(front, back) {
      const g = grid(GW, GH);
      // Ведомая (задняя) нога: смещение back, полупрозрачная.
      const bx = 17 + back;
      setA(g, bx, 49, C.skinShade, 0.45);
      setA(g, bx + 1, 49, C.skinShade, 0.45);
      setA(g, bx + 2, 49, C.skinShade, 0.45);
      rect(g, bx, 50, 3, 3, C.skinShade);
      rect(g, bx, 52, 3, 2, C.sock);
      rect(g, bx - 2, 54, 5, 2, C.shoe);
      // Передняя нога: смещение front, полная яркость.
      const fx = 21 + front;
      rect(g, fx, 49, 3, 3, C.skin);
      rect(g, fx, 52, 3, 2, C.sock);
      rect(g, fx - 2, 54, 5, 2, C.shoe);
      outline(g, C.outline);
      return g;
    }
    const A = mk(3, -2);   // правая вперёд, левая назад
    // Полный цикл шага = А и ЕГО зеркало (ноги врозь вправо / ноги врозь
    // влево). Раньше вторым кадром шёл mk(-2, 3): в нём обе ноги стояли в
    // центре под юбкой и сливались — на ходу ноги «пропадали» каждый второй
    // кадр.
    return [A, flipX(A)];
  }

  // setA — пиксель с альфой: «ведомая» нога пишется полупрозрачной, как в
  // собственной тени тела. paint() такие пиксели рисует с alphaFill.
  function setA(g, x, y, c, a) {
    x = Math.round(x);
    y = Math.round(y);
    if (x >= 0 && y >= 0 && x < g.w && y < g.h) g.px[y * g.w + x] = { c: c, a: a };
  }

  function bodyGrid(pose, legFrame) {
    const g = grid(GW, GH);
    const sitting = pose === 'sit';

    // Волосы сзади: то, что видно за головой и плечами.
    ell(g, 20, 16, 11.5, 12, C.hairBack);

    if (sitting) {
      // Сидя: колени в стороны, голени вниз, стопы у самого низа сетки. Низ
      // спрайта — это то место, на котором она сидит, поэтому окно ставится
      // низом на край панели задач (см. js/seat.js).
      rect(g, 11, 45, 7, 4, C.skinShade);   // левое бедро
      rect(g, 22, 45, 7, 4, C.skin);        // правое бедро
      rect(g, 12, 49, 4, 4, C.sock);        // левая голень
      rect(g, 24, 49, 4, 4, C.sock);        // правая голень
      rect(g, 11, 53, 6, 3, C.shoe);        // левая туфля
      rect(g, 24, 53, 6, 3, C.shoe);        // правая туфля
    } else if (legFrame >= 0) {
      // Шагающие ноги: кадр из листа шага (legGrids). Кадр — сетка целиком в
      // АБСОЛЮТНЫХ координатах (ноги стоят на своих строках 49..55), поэтому
      // копируется без смещений. Раньше копирование шло со сдвигом
      // (6 + x, WALK_LEG_Y + y): строки 49+48=97 уезжали за край сетки, и
      // при ходьбе ноги не дорисовывались вообще («ноги пропадают, когда она
      // бегает по экрану»).
      const lg = legGrids()[legFrame % 2];
      for (let y = 0; y < lg.h; y++) {
        for (let x = 0; x < lg.w; x++) {
          const v = lg.px[y * lg.w + x];
          if (v === null) continue;
          if (typeof v === 'object') setA(g, x, y, v.c, v.a);
          else set(g, x, y, v);
        }
      }
    } else {
      // Ноги, носки, туфли.
      rect(g, 17, 49, 3, 4, C.skinShade);
      rect(g, 21, 49, 3, 4, C.skin);
      rect(g, 17, 51, 3, 3, C.sock);
      rect(g, 21, 51, 3, 3, C.sock);
      rect(g, 15, 53, 5, 3, C.shoe);
      rect(g, 20, 53, 5, 3, C.shoe);
    }

    // Юбка-трапеция со складками. Сидя она короче: юбка ложится на бёдра и НЕ
    // свисает ниже них, иначе поза читалась бы как «стоит, но короткие ноги».
    const skirtTop = 40;
    const skirtBottom = sitting ? 46 : 49;
    for (let y = skirtTop; y <= skirtBottom; y++) {
      const hw = 5.5 + (y - skirtTop) * 0.42;
      rect(g, Math.round(20 - hw), y, Math.round(hw * 2), 1, C.skirt);
    }
    for (let x = 12; x <= 28; x += 3) {
      for (let y = skirtTop + 2; y <= skirtBottom; y++) {
        const tx = Math.round(x + (y - skirtTop - 2) * 0.3);
        if (g.px[y * GW + tx] === C.skirt) set(g, tx, y, C.skirtShade);
      }
    }

    // Блуза: плечи шире талии.
    for (let y = 28; y <= 40; y++) {
      const hw = 7.5 - (y - 28) * 0.11;
      rect(g, Math.round(20 - hw), y, Math.round(hw * 2), 1, C.blouse);
    }
    // Воротник уголком и лента на груди.
    for (let i = 0; i < 4; i++) {
      set(g, 20 - i, 28 + i, C.blouseShade);
      set(g, 20 + i, 28 + i, C.blouseShade);
    }
    rect(g, 18, 33, 5, 3, C.ribbon);
    set(g, 17, 33, C.ribbonShade);
    set(g, 17, 34, C.ribbonShade);
    set(g, 23, 33, C.ribbonShade);
    set(g, 23, 34, C.ribbonShade);
    set(g, 16, 34, C.ribbon);
    set(g, 24, 34, C.ribbon);

    // Шея.
    rect(g, 18, 23, 5, 5, C.skinShade);

    // Голова с ушками.
    ell(g, 20, 14, 9.5, 10, C.skin);
    set(g, 10, 14, C.skin); set(g, 10, 15, C.skinShade);
    set(g, 30, 14, C.skin); set(g, 30, 15, C.skinShade);

    // Затенение у линии волос: без него лицо выглядит плоским.
    for (let x = 13; x <= 27; x++) {
      if (g.px[LAYOUT.faceTop * GW + x] === C.skin) set(g, x, LAYOUT.faceTop, C.skinShade);
    }

    // Чёлка: закрывает лоб и заканчивается ВЫШЕ глаз (иначе пряди съедали лицо).
    ell(g, 20, 6, 10.5, 6.5, C.hairBase);
    for (const bx of [11, 14, 17, 23, 26, 29]) {
      line(g, bx, 9, bx + 1, 12, C.hairBase);
    }
    ell(g, 16, 4, 4.5, 2, C.hairLight);
    ell(g, 25, 5, 2.5, 1.4, C.hairBack);

    // Ахоге — торчащая прядь: без неё это «девочка», с ней — аниме-девочка.
    line(g, 22, 3, 25, 0, C.hairBase);
    set(g, 26, 0, C.hairBase);

    // Лента сбоку головы.
    rect(g, 9, 7, 5, 4, C.ribbon);
    set(g, 9, 6, C.ribbonShade);
    set(g, 13, 6, C.ribbonShade);
    set(g, 8, 11, C.ribbonShade);
    set(g, 14, 11, C.ribbonShade);
    rect(g, 11, 8, 2, 2, C.ribbonShade);

    outline(g, C.outline);
    return g;
  }

  // Прядь волос по боку: отдельная часть, потому что она КАЧАЕТСЯ (поворот
  // вокруг верхней точки крепления). Ширина 8: шире — и прядь закрывает лицо.
  function tailGrid() {
    const g = grid(8, 36);
    for (let y = 0; y < 36; y++) {
      const hw = 3.4 - y * 0.05;
      rect(g, Math.round(4 - hw), y, Math.round(hw * 2), 1, C.hairBase);
    }
    rect(g, 2, 2, 2, 8, C.hairLight);
    for (let y = 1; y < 33; y++) {
      if (g.px[y * 8 + 5] !== null) set(g, 5, y, C.hairBack);
    }
    outline(g, C.outline);
    return g;
  }

  // outlineArm — контур руки ТОЛЬКО с внешней стороны и снизу.
  //
  // Почему не полная обводка: она рисует тёмную линию по всему краю руки, и на
  // плече появляется шов — рука выглядит приклеенной отдельно от тела. Внешняя
  // сторона обведена (рука читается), внутренняя остаётся «пришитой».
  function outlineArm(g, c, dir) {
    const src = g.px.slice();
    const at = (x, y) => (x < 0 || y < 0 || x >= g.w || y >= g.h ? null : src[y * g.w + x]);
    for (let y = 0; y < g.h; y++) {
      for (let x = 0; x < g.w; x++) {
        if (src[y * g.w + x] === null) continue;
        if (at(x + dir, y) === null || at(x, y + 1) === null) g.px[y * g.w + x] = c;
      }
    }
  }

  // Рука опущена: рукав, рука, кисть. dir — куда смотрит внешняя сторона
  // (-1 для левой руки, +1 для правой).
  function armGrid(dir) {
    const g = grid(6, 16);
    rect(g, 0, 0, 5, 4, C.blouse);
    rect(g, 1, 4, 3, 9, C.skin);
    rect(g, 1, 12, 3, 3, C.skinShade);
    outlineArm(g, C.outline, dir);
    return g;
  }

  // Рука ПОДНЯТА — взмах. Сетка нарисована ОТ ПЛЕЧА: левый нижний угол — это
  // плечо, поэтому контейнер можно поставить точно в плечо, и поворот вокруг
  // начала координат не отрывает руку от тела.
  function armUpGrid() {
    const g = grid(12, 22);
    rect(g, 0, 16, 5, 6, C.blouse);          // рукав у плеча
    for (let y = 5; y <= 16; y++) {          // рука вверх-вправо
      const x = 1 + (16 - y) * 0.42;
      rect(g, Math.round(x), y, 3, 1, C.skin);
    }
    rect(g, 6, 1, 4, 4, C.skin);             // кисть
    set(g, 6, 0, C.skin);
    set(g, 9, 0, C.skin);
    set(g, 10, 4, C.skin);
    outline(g, C.outline);
    return g;
  }

  // Пиксельное сердце — для поглаживания по голове.
  function heartGrid() {
    const g = grid(9, 8);
    ell(g, 2.6, 2.6, 2.6, 2.6, C.heart);
    ell(g, 6.4, 2.6, 2.6, 2.6, C.heart);
    rect(g, 0, 2, 9, 3, C.heart);
    for (let y = 5; y <= 7; y++) {
      const pad = (y - 4) * 2;
      rect(g, pad, y, 9 - pad * 2, 1, C.heart);
    }
    outline(g, C.heartDark);
    return g;
  }

  // Буква «Z» для спящей Миры: читается при размере клетки 2 px — поэтому
  // диагональ идёт ступеньками по одному пикселю (двойная ширина не нужна,
  // при мелкой клетке она бы сливалась в кашу).
  function zzzGrid() {
    const g = grid(7, 8);
    rect(g, 0, 0, 7, 1, C.zzz);
    for (let i = 1; i <= 5; i++) set(g, 6 - i, i, C.zzz);
    rect(g, 0, 6, 7, 1, C.zzz);
    outline(g, C.zzzDark);
    return g;
  }

  // Глаз: белок, большая радужка, тень радужки снизу и ресница по верхнему краю.
  //
  // Размеры подобраны так, чтобы глаз оставался ОТКРЫТЫМ: когда ресница занимала
  // два ряда сверху, лицо выглядело сонным при любом настроении.
  function eyeGrid() {
    const g = grid(9, 9);
    ell(g, 4, 4.6, 4.2, 4.1, C.eyeWhite);
    // Радужка сдвинута ВНИЗ: сверху остаётся полоска белка, и глаз смотрит
    // открыто. Когда радужка стояла по центру, глаза читались прищуренными.
    ell(g, 4, 5.4, 2.9, 3.0, C.iris);
    ell(g, 4, 6.4, 2.1, 1.5, C.irisDark);
    rect(g, 0, 0, 9, 1, C.lash);
    set(g, 0, 1, C.lash);
    set(g, 8, 1, C.lash);
    // Блик: два светлых пикселя в верхнем левом углу радужки — без него глаз
    // выглядит нарисованным, а не живым.
    set(g, 2, 2, C.eyeWhite);
    set(g, 2, 3, C.eyeWhite);
    set(g, 3, 2, C.eyeWhite);
    return g;
  }

  function pupilGrid() {
    const g = grid(9, 9);
    ell(g, 4, 5.4, 1.5, 1.7, C.pupil);
    return g;
  }

  // Прикрытый глаз — моргание и сон.
  function eyeClosedGrid() {
    const g = grid(9, 9);
    rect(g, 0, 5, 9, 1, C.lash);
    set(g, 1, 4, C.lash);
    set(g, 7, 4, C.lash);
    return g;
  }

  // Радостный прищур: глаз «галочкой».
  function eyeHappyGrid() {
    const g = grid(9, 9);
    set(g, 0, 6, C.lash);
    set(g, 1, 5, C.lash);
    rect(g, 2, 4, 5, 1, C.lash);
    set(g, 7, 5, C.lash);
    set(g, 8, 6, C.lash);
    return g;
  }

  // Брови: спокойные (по умолчанию спрятаны), грустные, задумчивые.
  function browGrid(kind) {
    const g = grid(7, 3);
    if (kind === 'sad') {
      set(g, 0, 0, C.hairBack);
      set(g, 1, 1, C.hairBack);
      rect(g, 2, 2, 5, 1, C.hairBack);
      return g;
    }
    if (kind === 'think') {
      rect(g, 0, 1, 6, 1, C.hairBack);
      set(g, 6, 0, C.hairBack);
      return g;
    }
    rect(g, 0, 1, 7, 1, C.hairBack);
    return g;
  }

  // Рот: спокойный, улыбка, грусть, задумчивость, открытый (разговор), сон.
  function mouthGrid(kind) {
    const g = grid(8, 4);
    switch (kind) {
      case 'happy':
        set(g, 1, 1, C.mouth);
        rect(g, 2, 2, 4, 1, C.mouth);
        set(g, 6, 1, C.mouth);
        break;
      case 'sad':
        rect(g, 2, 1, 4, 1, C.mouth);
        set(g, 1, 2, C.mouth);
        set(g, 6, 2, C.mouth);
        break;
      case 'think':
        rect(g, 2, 2, 4, 1, C.mouth);
        break;
      case 'open':
        ell(g, 3.5, 1.8, 1.8, 1.9, C.mouth);
        break;
      case 'sleep':
        ell(g, 3.5, 2, 1.2, 1.1, C.mouth);
        break;
      default:
        rect(g, 2, 2, 4, 1, C.mouth);
    }
    return g;
  }

  // --- сборка персонажа ----------------------------------------------------

  function create(PIXI, opts) {
    // Насколько крупнее внутренней сетки рисуется спрайт. 1 = как нарисовано.
    let scale = 1;
    const root = new PIXI.Container();

    // Тень под ногами: без неё персонаж висит в воздухе прозрачного окна.
    const shadow = new PIXI.Graphics();
    shadow.beginFill(0x000000, 0.16);
    shadow.drawEllipse(0, SPRITE_H / 2 + 2, SPRITE_W * 0.26, SPRITE_H * 0.016);
    shadow.endFill();
    root.addChild(shadow);

    // Спрайт целиком: начало координат — его центр.
    const sprite = new PIXI.Container();
    sprite.position.set(-SPRITE_W / 2, -SPRITE_H / 2);
    root.addChild(sprite);

    // Пряди по бокам: качаются. Точка вращения — там, где прядь «растёт»
    // (у головы), поэтому контейнер сдвигаем на pivotX, а прядь внутри — назад.
    function makeTail(grid, gx, gy, pivotX) {
      const wrap = new PIXI.Container();
      wrap.position.set((gx + pivotX) * PIX, gy * PIX);
      const gfx = paint(PIXI, grid, PIX);
      gfx.position.set(-pivotX * PIX, 0);
      wrap.addChild(gfx);
      sprite.addChild(wrap);
      return wrap;
    }

    // Тело — единственная часть, которая перерисовывается при смене позы
    // (стоя/сидя/шаг): лицо, глаза, руки и пряди стоят в любой позе на своих
    // местах, поэтому перерисовка касается ровно одного объекта.
    let body = paint(PIXI, bodyGrid('stand', -1), PIX);
    sprite.addChild(body);

    // Пряди вынесены за лицо (x 2..10 и 30..38): когда они доходили до середины,
    // они съедали левый глаз.
    const tailL = makeTail(flipX(tailGrid()), 1, 9, 5);
    const tailR = makeTail(tailGrid(), 31, 9, 3);
    tailL.rotation = 0.03;
    tailR.rotation = -0.03;

    // Руки. Обводка у них только с внешней стороны (см. outlineArm): полная
    // обводка рисует шов на плече, и рука выглядит приклеенной к телу снаружи.
    const armL = paint(PIXI, armGrid(-1), PIX);
    armL.position.set(8 * PIX, 28 * PIX);
    const armR = paint(PIXI, armGrid(1), PIX);
    armR.position.set(27 * PIX, 28 * PIX);
    sprite.addChild(armL);
    sprite.addChild(armR);

    // Поднятая рука: контейнер стоит РОВНО в плече (26,28), а сетка внутри
    // сдвинута так, чтобы её левый нижний угол пришёлся на эту точку. Поэтому
    // вращение идёт вокруг плеча, и рука не отъезжает от тела.
    const armUpWrap = new PIXI.Container();
    armUpWrap.position.set(26 * PIX, 28 * PIX);
    const armUp = paint(PIXI, armUpGrid(), PIX);
    armUp.position.set(0, -21 * PIX);
    armUpWrap.addChild(armUp);
    armUpWrap.visible = false;
    sprite.addChild(armUpWrap);

    // Зеркальная поднятая левая рука (для потягивания): сетка та же,
    // отражённая; плечо — правый нижний угол, поэтому сдвиг внутрь на
    // (−11, −21), и вращение идёт вокруг левого плеча (13,28).
    const armUpLeftWrap = new PIXI.Container();
    armUpLeftWrap.position.set(13 * PIX, 28 * PIX);
    const armUpLeft = paint(PIXI, flipX(armUpGrid()), PIX);
    armUpLeft.position.set(-11 * PIX, -21 * PIX);
    armUpLeftWrap.addChild(armUpLeft);
    armUpLeftWrap.visible = false;
    sprite.addChild(armUpLeftWrap);

    // --- лицо: глаза, брови, рот, румянец --------------------------------
    function facePart(gfx, gx, gy) {
      const holder = new PIXI.Container();
      holder.position.set(gx * PIX, gy * PIX);
      holder.addChild(gfx);
      sprite.addChild(holder);
      return holder;
    }

    function makeEye(gx, gy) {
      const holder = facePart(paint(PIXI, eyeGrid(), PIX), gx, gy);
      const pupil = paint(PIXI, pupilGrid(), PIX);
      holder.addChild(pupil);
      return { holder: holder, pupil: pupil };
    }

    const left = makeEye(LAYOUT.eyeLX, LAYOUT.eyeY);
    const right = makeEye(LAYOUT.eyeRX, LAYOUT.eyeY);

    const closedL = facePart(paint(PIXI, eyeClosedGrid(), PIX), LAYOUT.eyeLX, LAYOUT.eyeY);
    const closedR = facePart(paint(PIXI, eyeClosedGrid(), PIX), LAYOUT.eyeRX, LAYOUT.eyeY);
    const happyL = facePart(paint(PIXI, eyeHappyGrid(), PIX), LAYOUT.eyeLX, LAYOUT.eyeY);
    const happyR = facePart(paint(PIXI, eyeHappyGrid(), PIX), LAYOUT.eyeRX, LAYOUT.eyeY);
    closedL.visible = false;
    closedR.visible = false;
    happyL.visible = false;
    happyR.visible = false;

    const brows = {
      calmL: facePart(paint(PIXI, browGrid('calm'), PIX), LAYOUT.eyeLX, LAYOUT.browY),
      calmR: facePart(paint(PIXI, browGrid('calm'), PIX), LAYOUT.eyeRX, LAYOUT.browY),
      sadL: facePart(paint(PIXI, browGrid('sad'), PIX), LAYOUT.eyeLX, LAYOUT.browY),
      sadR: facePart(paint(PIXI, flipX(browGrid('sad')), PIX), LAYOUT.eyeRX, LAYOUT.browY),
      thinkL: facePart(paint(PIXI, browGrid('think'), PIX), LAYOUT.eyeLX, LAYOUT.browY),
      thinkR: facePart(paint(PIXI, flipX(browGrid('think')), PIX), LAYOUT.eyeRX, LAYOUT.browY),
    };
    for (const key of Object.keys(brows)) brows[key].visible = false;

    const mouthHolder = new PIXI.Container();
    mouthHolder.position.set(16 * PIX, LAYOUT.mouthY * PIX);
    sprite.addChild(mouthHolder);

    const mouths = {
      calm: paint(PIXI, mouthGrid('calm'), PIX),
      happy: paint(PIXI, mouthGrid('happy'), PIX),
      sad: paint(PIXI, mouthGrid('sad'), PIX),
      think: paint(PIXI, mouthGrid('think'), PIX),
      open: paint(PIXI, mouthGrid('open'), PIX),
      sleep: paint(PIXI, mouthGrid('sleep'), PIX),
    };
    for (const key of Object.keys(mouths)) {
      mouths[key].visible = false;
      mouthHolder.addChild(mouths[key]);
    }
    mouths.calm.visible = true;

    // Румянец — полупрозрачными пятнами, а не пиксельной сеткой: так он читается
    // как нежный, а не как две красные заплатки.
    const blush = new PIXI.Graphics();
    for (const s of [-1, 1]) {
      blush.beginFill(C.blush, 0.45);
      blush.drawEllipse(20 * PIX + s * 7.6 * PIX, 19 * PIX, 2.4 * PIX, 1.4 * PIX);
      blush.endFill();
    }
    sprite.addChild(blush);

    // --- сердечки при поглаживании ---------------------------------------
    // Отдельный слой НАД спрайтом: сердечки улетают от головы вверх и не должны
    // быть перекрыты телом.
    const heartLayer = new PIXI.Container();
    root.addChild(heartLayer);
    // Сердечко мельче тела (px=3): крупные сердечки перекрывали пол-окна и
    // читались кашей, а не нежностью.
    const heartTex = paint(PIXI, heartGrid(), 3);
    const hearts = [];

    // --- сон: буквы «Z», улетающие от головы ------------------------------
    // Тот же приём, что у сердечек, только летят медленнее и с просветами:
    // сонные «Z» не должны мелькать пулемётом.
    const zzzTex = paint(PIXI, zzzGrid(), 3);
    const zzzs = [];

    // --- состояние --------------------------------------------------------
    const anim = {
      emotion: 'calm',
      pose: 'stand',     // 'stand' | 'sit'
      t: 0,
      baseY: 0,
      blinkIn: 2 + Math.random() * 3,
      blinkT: 0,
      blinking: false,
      asleep: false,
      talkUntil: 0,
      talkFlip: 0,
      waving: 0,
      lookX: 0,
      lookY: 0,
      // Поглаживание: поток сердечек и улыбка на несколько секунд.
      petStream: 0,
      petTimer: 0,
      happyUntil: 0,
      // Прогулка: направление (1 вправо, -1 влево, 0 стоим) и кадр шага.
      walking: 0,
      walkFrame: 0,
      walkSwap: 0,
      // Режим внешнего листа (этап 6): открыт ли рот в текущем такте речи —
      // по нему лист выбирает кадр «говорит».
      talkOpen: false,
      // Сон: очередь «Z» и таймер следующей буквы.
      zzzTimer: 0,
      // Полоска настроения (этап 5): 'low' | 'mid' | 'high'. По ней
      // выбирается «лицо покоя» — куда лицо возвращается, когда нет эмоций
      // от событий (улыбка после поглаживания, «думает», сон).
      moodBand: 'mid',
      // Танец под музыку (этап «реакция на звук»): включён, частота
      // подскока (рад/с, из измеренного детектором темпа) и фаза рук.
      dance: false,
      danceW: 6.3,
      danceT: 0,
      // Микроживость (этап 9а): текущее микродвижение {kind, t, dur} и
      // таймер до следующего (только в бодром простое).
      fidget: null,
      fidgetIn: 25 + Math.random() * 40,
      // Эмпатия (этап 9в): лицо-реакция на состояние человека
      // {face, applied, until}: применяется, когда она договорила ответ,
      // и гаснет через несколько секунд — сочувствие, а не маска.
      userReact: null,
    };

    function showEyes(kind) {
      if (sheet) return;   // в режиме листа лицо — это сам кадр, не накладка
      const open = kind === 'open';
      left.holder.visible = open;
      right.holder.visible = open;
      closedL.visible = kind === 'closed';
      closedR.visible = kind === 'closed';
      happyL.visible = kind === 'happy';
      happyR.visible = kind === 'happy';
    }

    function showBrows(kind) {
      if (sheet) return;
      for (const key of Object.keys(brows)) brows[key].visible = false;
      if (kind === 'sad') {
        brows.sadL.visible = true;
        brows.sadR.visible = true;
      } else if (kind === 'think') {
        brows.thinkL.visible = true;
        brows.thinkR.visible = true;
      }
    }

    function setMouth(kind) {
      if (sheet) return;
      for (const key of Object.keys(mouths)) mouths[key].visible = key === kind;
    }

    function mouthFor(emotion) {
      switch (emotion) {
        case 'happy': return 'happy';
        case 'sad': return 'sad';
        case 'think': return 'think';
        case 'sleep': return 'sleep';
        default: return 'calm';
      }
    }

    function setEmotion(name) {
      if (EMOTIONS.indexOf(name) < 0) return anim.emotion;
      anim.emotion = name;
      anim.asleep = name === 'sleep';

      switch (name) {
        case 'happy':
          showEyes('happy');
          showBrows('none');
          blush.alpha = 1;
          break;
        case 'sad':
          showEyes('open');
          showBrows('sad');
          blush.alpha = 0.35;
          break;
        case 'think':
          showEyes('open');
          showBrows('think');
          blush.alpha = 0.5;
          break;
        case 'sleep':
          showEyes('closed');
          showBrows('none');
          blush.alpha = 0.4;
          break;
        default:
          showEyes('open');
          showBrows('none');
          blush.alpha = 0.6;
      }
      setMouth(mouthFor(name));
      if (sheet) syncSheetFrame(true);
      return anim.emotion;
    }

    // calmFace — «лицо покоя» по полоске настроения: куда лицу возвращаться,
    // когда событие (улыбка, «думает», сон) закончилось. Настроение грустное —
    // лицо грустное, а не дежурное: «настроение упало, лицо нейтральное» —
    // это ровно то расхождение, которое видно глазами.
    function calmFace() {
      if (anim.moodBand === 'low') return 'sad';
      if (anim.moodBand === 'high') return 'happy';
      return 'calm';
    }

    // setMoodBand — полоска настроения (этап 5). «Лицо покоя» меняется, если
    // лицо сейчас именно из покоя: спокойное или то, что полоска уже
    // выбрала сама (грустное/лучезарное от настроения). Событийные лица —
    // сон, улыбка от поглаживания, «думает», разговор — полоской не
    // перебиваются: событие важнее настроения и доживёт до конца.
    function setMoodBand(band) {
      if (band !== 'low' && band !== 'mid' && band !== 'high') return anim.moodBand;
      const prevFace = calmFace();
      anim.moodBand = band;
      if (anim.emotion === 'calm' || anim.emotion === prevFace) {
        setEmotion(calmFace());
      }
      return anim.moodBand;
    }

    // say — показать, что она ГОВОРИТ: рот открывается и закрывается столько,
    // сколько примерно заняло бы произнесение реплики.
    function say(text) {
      const len = (text || '').length;
      anim.talkUntil = Math.max(anim.talkUntil, anim.t + Math.min(7, Math.max(1.2, len * 0.045)));
      if (anim.emotion !== 'sleep') setEmotion('happy');
    }

    function wave() {
      anim.waving = 1.8;
      if (sheet) { syncSheetFrame(true); return; }   // лист: кадр «машет рукой»
      armUpWrap.visible = true;
      armR.visible = false;
    }

    // --- микроживость (этап 9а) --------------------------------------------
    // В бодром простое она не «статуя»: иногда потянется, почешет затылок,
    // ёрзнет на месте, посмотрит на свою ладошку. Движения маленькие и
    // идут по синусной огибающей (0→1→0): начало и конец плавные, без
    // «включил анимацию». Речь, драг, танец, шаг и сон перекрывают
    // микроживость; в режиме листа её нет — там кадры выбирает лист.
    const FIDGETS = {
      stretch: { dur: 2.6, p: 0.30 },   // потягивается: обе руки вверх
      scratch: { dur: 2.0, p: 0.25 },   // чешет затылок
      squirm: { dur: 1.1, p: 0.25 },    // ёрзает
      palm: { dur: 2.4, p: 0.20 },      // смотрит на ладошку
    };
    const PIVX = 20 * PIX, PIVY = 40 * PIX;   // ось наклона — талия

    // pivotRotate — наклон ВСЕГО спрайта вокруг талии: контейнер стоит в
    // (-20,-28) и вращается вокруг своего левого верхнего угла, поэтому
    // положение компенсируется поворотом точки крепления.
    function pivotRotate(theta) {
      const dx = -SPRITE_W / 2 - PIVX, dy = -SPRITE_H / 2 - PIVY;
      const c = Math.cos(theta), s = Math.sin(theta);
      sprite.rotation = theta;
      sprite.position.set(PIVX + dx * c - dy * s, PIVY + dx * s + dy * c);
    }

    function startFidget(kind) {
      if (!FIDGETS[kind]) return null;
      anim.fidget = { kind: kind, t: 0, dur: FIDGETS[kind].dur };
      if (kind === 'stretch') {
        armUpWrap.visible = true; armR.visible = false;
        armUpLeftWrap.visible = true; armL.visible = false;
      } else if (kind === 'scratch' || kind === 'palm') {
        armUpWrap.visible = true; armR.visible = false;
      }
      return kind;
    }

    function endFidget() {
      anim.fidget = null;
      armUpWrap.visible = false; armUpWrap.rotation = 0;
      armUpLeftWrap.visible = false;
      armL.visible = true; armR.visible = true;
      armL.rotation = 0; armR.rotation = 0;
      pivotRotate(0);
      anim.lookX = 0; anim.lookY = 0;
    }

    // spawnHeart — одно сердечко, улетающее от головы. Плавно тает: резкое
    // исчезновение выглядит как выключенный эффект, а не как закончившееся
    // движение.
    function spawnHeart() {
      const heart = heartTex.clone();
      heart.x = (Math.random() - 0.5) * SPRITE_W * 0.6;
      heart.y = -SPRITE_H / 2 - 6 + Math.random() * 18;
      heart.alpha = 0.95;
      heartLayer.addChild(heart);
      hearts.push({
        gfx: heart,
        vy: -32 - Math.random() * 18,
        vx: 6 + Math.random() * 10,
        life: 1.5,
        age: 0,
        phase: Math.random() * Math.PI * 2,
      });
    }

    // spawnZzz — одна «Z» над головой спящей Миры. Летит правее и медленнее
    // сердечка, живёт дольше: сон — не всплеск эмоций, а медленное дыхание.
    function spawnZzz() {
      const z = zzzTex.clone();
      z.x = SPRITE_W * 0.16 + Math.random() * SPRITE_W * 0.12;
      z.y = -SPRITE_H / 2 - 4 + Math.random() * 10;
      z.alpha = 0.9;
      heartLayer.addChild(z);
      zzzs.push({
        gfx: z,
        vy: -16 - Math.random() * 8,
        vx: 10 + Math.random() * 8,
        life: 2.2,
        age: 0,
        phase: Math.random() * Math.PI * 2,
      });
    }

    // pet — её гладят по голове: сердечки и улыбка. Зовётся повторно, пока
    // гладят (см. js/pet.js), поэтому поток сердечек не прекращается, пока
    // человек не остановится.
    function pet() {
      anim.petStream = Math.max(anim.petStream, 0.9);
      anim.happyUntil = anim.t + 3;
      if (anim.emotion !== 'happy' && anim.emotion !== 'sleep') setEmotion('happy');
    }

    function lookAt(px, py) { return lookPower(px, py, 1); }

    // Сила слежения (0..1): на сколько зрачки доезжают до упора. На нуле
    // взгляд не едет за курсором НИКУДА — зрачки возвращаются к середине
    // (глаза смотрят прямо), а не залипают на последней точке.
    function lookPower(px, py, power) {
      // Зрачки смещаются максимум на один пиксель спрайта: больше — и они
      // выезжают из глаз, что выглядит поломкой, а не взглядом.
      const p = Math.max(0, Math.min(1, power));
      const d = Math.hypot(px, py) || 1;
      const k = Math.min(1, d / (SPRITE_W * scale * 0.5)) * PIX * p;
      anim.lookX = (px / d) * k;
      anim.lookY = (py / d) * k;
    }

    function update(dtMs) {
      const dt = Math.min(0.05, dtMs / 1000);
      anim.t += dt;

      // Дыхание: лёгкое покачивание всего тела. На шаге покачивание
      // синхронизируется с кадрами ног: тело поднимается на каждом шаге
      // (0.26 с — период кадра, частота 2π/0.26), и без этого персонаж
      // «плывёт» над полом на ножках-шпильках.
      root.y = anim.baseY +
        Math.sin(anim.t * (anim.walking !== 0 ? 24.2 : 1.7)) * SPRITE_H *
          (anim.walking !== 0 ? 0.012 : 0.006);

      // Танец под музыку: тело подпрыгивает в такт измеренному темпу
      // (частоту передаёт детектор js/music.js). Спьяна и на ходу не
      // танцует: покачивание шага и сон главнее.
      if (anim.dance && anim.walking === 0 && !anim.asleep) {
        root.y = anim.baseY + Math.sin(anim.t * anim.danceW) * SPRITE_H * 0.022;
      }

      // Волосы качаются медленнее дыхания — так движение выглядит живым.
      // Под музыку качаются сильнее и в такт: на маленьком спрайте это
      // главное, что видно из танца.
      if (anim.dance && !anim.asleep) {
        tailL.rotation = 0.05 + Math.sin(anim.t * anim.danceW * 0.5) * 0.07;
        tailR.rotation = -0.05 - Math.sin(anim.t * anim.danceW * 0.5 + 0.5) * 0.07;
      } else {
        tailL.rotation = 0.03 + Math.sin(anim.t * 1.1) * 0.035;
        tailR.rotation = -0.03 - Math.sin(anim.t * 1.1 + 0.6) * 0.035;
      }

      // Моргание.
      if (!anim.asleep && anim.talkUntil < anim.t) {
        anim.blinkIn -= dt;
        if (anim.blinkIn <= 0) {
          anim.blinkT = 0.12;
          anim.blinkIn = 2.5 + Math.random() * 3.5;
        }
        const blinking = anim.blinkT > 0;
        if (blinking) anim.blinkT -= dt;
        if (blinking !== anim.blinking) {
          anim.blinking = blinking;
          // В режиме листа веки — часть кадра, моргание выбирает кадр «eyes
          // closed»; таймер моргания при этом работает как обычно.
          if (sheet) { /* кадр подберёт syncSheetFrame() */ }
          else if (blinking) {
            closedL.visible = true;
            closedR.visible = true;
            left.holder.visible = false;
            right.holder.visible = false;
          } else {
            closedL.visible = false;
            closedR.visible = false;
            showEyes(anim.emotion === 'happy' ? 'happy' : (anim.asleep ? 'closed' : 'open'));
          }
        }
      }

      // Разговор: рот открыт примерно половину времени.
      if (anim.talkUntil > anim.t) {
        anim.talkFlip += dt;
        const open = anim.talkFlip % 0.32 < 0.16;
        anim.talkOpen = open;
        mouths.open.visible = open;
        if (open) {
          mouths.calm.visible = false;
          mouths.happy.visible = false;
          mouths.sad.visible = false;
          mouths.think.visible = false;
        } else {
          setMouth(mouthFor(anim.emotion));
        }
      } else if (mouths.open.visible) {
        mouths.open.visible = false;
        anim.talkOpen = false;
        setMouth(mouthFor(anim.emotion));
      }

      // Взмах рукой: поднятая рука покачивается ВОКРУГ ПЛЕЧА, потом опускается.
      // Амплитуда маленькая (0,09 радиана): рука длиной в сто пикселей на
      // большем угле начинает жить отдельной от тела жизнью.
      if (anim.waving > 0) {
        anim.waving -= dt;
        if (!sheet) armUpWrap.rotation = Math.sin(anim.t * 9) * 0.09;
        if (anim.waving <= 0 && !sheet) {
          armUpWrap.visible = false;
          armR.visible = true;
          armUpWrap.rotation = 0;
        }
      }

      // Рука в танце: поднята полтакта, опущена полтакта — «раз-два».
      // Взмах по главному клику и внешний лист рукавами в танце не
      // занимаются (лист сам выбирает кадр).
      if (anim.dance && anim.waving <= 0 && !sheet) {
        anim.danceT += dt;
        if (Math.sin(anim.danceT * anim.danceW * 0.5) > 0) {
          armUpWrap.visible = true;
          armR.visible = false;
          armUpWrap.rotation = Math.sin(anim.t * anim.danceW) * 0.14;
        } else {
          armUpWrap.visible = false;
          armR.visible = true;
          armUpWrap.rotation = 0;
        }
      }

      // Микроживость: в бодром простое — случайные микродвижения
      // (потянуться, почесать затылок, ёрзнуть, посмотреть на ладошку).
      // Речь, шаг, танец, взмах и сон её обрывают, в режиме листа её нет.
      if (anim.fidget &&
          (sheet || anim.asleep || anim.walking !== 0 || anim.dance || anim.waving > 0)) {
        endFidget();
      }
      if (!anim.fidget && !sheet && !anim.asleep && anim.walking === 0 &&
          !anim.dance && anim.waving <= 0 && anim.talkUntil < anim.t) {
        anim.fidgetIn -= dt;
        if (anim.fidgetIn <= 0) {
          const r = Math.random();
          const kind = r < 0.30 ? 'stretch' : r < 0.55 ? 'scratch' :
            r < 0.80 ? 'squirm' : 'palm';
          startFidget(kind);
          anim.fidgetIn = 30 + Math.random() * 60;
        }
      }
      if (anim.fidget) {
        const f = anim.fidget;
        f.t += dt;
        const e = Math.sin(Math.PI * Math.min(1, f.t / f.dur));   // 0→1→0
        if (f.kind === 'stretch') {
          pivotRotate(-0.05 * e);
          armUpWrap.rotation = -0.38 * e;
          armUpLeftWrap.rotation = 0.38 * e;
        } else if (f.kind === 'scratch') {
          pivotRotate(0.06 * e);
          armUpWrap.rotation = -0.55 * e;
        } else if (f.kind === 'squirm') {
          const w = Math.sin(f.t * 16) * 0.04 * e;
          pivotRotate(w);
          armL.rotation = -w; armR.rotation = w;
        } else {   // palm
          pivotRotate(0.10 * e);
          armUpWrap.rotation = -0.78 * e;
          anim.lookX = -0.7 * PIX * e;
          anim.lookY = -0.55 * PIX * e;
        }
        if (f.t >= f.dur) endFidget();
      }

      // Сердечки: поднимаются, покачиваются и тают.
      if (anim.petStream > 0) {
        anim.petStream -= dt;
        anim.petTimer -= dt;
        if (anim.petTimer <= 0) {
          spawnHeart();
          anim.petTimer = 0.18;
        }
      }

      // Шаг: смена кадра ног. 0.26 с — темп спокойной прогулки; быстрее
      // «перебирает», медленнее — «плывёт». Кадр живёт ровно половину цикла
      // покачивания тела (см. дыхание): 0.26 = π/12.1 — два шага за период.
      if (anim.walking !== 0) {
        anim.walkSwap -= dt;
        if (anim.walkSwap <= 0) {
          anim.walkFrame = anim.walkFrame ? 0 : 1;
          anim.walkSwap = 0.26;
          repaintBody();
        }
      }

      // «Z» идут, только пока она спит и молчит: заговорив во сне, она выдыхает
      // букву на полуслове, а не засыпает. Таймер обнуляем, чтобы первая «Z»
      // после пробуждения речи появилась не мгновенно.
      if (anim.asleep && anim.talkUntil < anim.t) {
        anim.zzzTimer -= dt;
        if (anim.zzzTimer <= 0) {
          spawnZzz();
          anim.zzzTimer = 0.9 + Math.random() * 0.7;
        }
      } else {
        anim.zzzTimer = 0;
      }
      for (let i = zzzs.length - 1; i >= 0; i--) {
        const z = zzzs[i];
        z.age += dt;
        z.gfx.y += z.vy * dt;
        z.gfx.x += Math.sin(anim.t * 1.6 + z.phase) * z.vx * dt;
        z.gfx.alpha = Math.max(0, 0.9 * (1 - z.age / z.life));
        if (z.age >= z.life) {
          heartLayer.removeChild(z.gfx);
          z.gfx.destroy();
          zzzs.splice(i, 1);
        }
      }
      for (let i = hearts.length - 1; i >= 0; i--) {
        const h = hearts[i];
        h.age += dt;
        h.gfx.y += h.vy * dt;
        h.gfx.x += Math.sin(anim.t * 3 + h.phase) * h.vx * dt;
        h.gfx.alpha = Math.max(0, 1 - h.age / h.life);
        if (h.age >= h.life) {
          heartLayer.removeChild(h.gfx);
          h.gfx.destroy();
          hearts.splice(i, 1);
        }
      }

      // Улыбка после поглаживания держится несколько секунд, потом лицо снова
      // спокойное: постоянная улыбка — это уже не эмоция, а маска.
      if (anim.happyUntil > 0 && anim.t > anim.happyUntil && anim.petStream <= 0 && anim.talkUntil < anim.t) {
        anim.happyUntil = 0;
        if (anim.emotion === 'happy') setEmotion(calmFace());
      }

      // Эмпатия (этап 9в): лицо-реакция на состояние человека. Применяется,
      // когда она договорила ответ (не перебивает речь), держится несколько
      // секунд и возвращается к «лицу покоя» полоски настроения.
      if (anim.userReact) {
        if (anim.asleep) {
          anim.userReact = null;   // сон не перекрашивают
        } else if (!anim.userReact.applied && anim.talkUntil < anim.t &&
                   anim.petStream <= 0) {
          anim.userReact.applied = true;
          anim.userReact.until = anim.t + 6;
          setEmotion(anim.userReact.face);
        } else if (anim.userReact.applied && anim.t >= anim.userReact.until) {
          anim.userReact = null;
          if (anim.talkUntil < anim.t && anim.petStream <= 0) setEmotion(calmFace());
        }
      }

      // Зрачки: плавно к цели.
      for (const eye of [left, right]) {
        eye.pupil.x += (anim.lookX - eye.pupil.x) * Math.min(1, dt * 10);
        eye.pupil.y += (anim.lookY - eye.pupil.y) * Math.min(1, dt * 10);
      }

      // Режим листа: в конце кадра смотрим, не сменилось ли состояние
      // (шаг, моргание, фаза речи, взмах) — и если да, ставим нужный кадр.
      if (sheet) syncSheetFrame();
    }

    // --- внешний лист кадров (этап 6: смена спрайтов) ----------------------
    //
    // Человек подкладывает свои PNG в папку арта (по умолчанию
    // %AppData%\Friend\sprites), сервер раздаёт её по /sprites/, и лист
    // оживает: персонаж целиком — это картинка текущего состояния, а
    // процедурные глаза, брови, рот, пряди и руки прячутся. Эффекты (шаг,
    // моргание, речь, взмах) не исчезают — они выбирают КАДР: лист живёт по
    // тем же состояниям, что и нарисованная кодом Мира.
    //
    // Кадр выбирается по цепочке важности: взмах > речь > моргание > шаг >
    // эмоция > поза > база. Отсутствующий кадр проваливается вниз цепочки
    // («спит», если есть только «sleep»-картинка; «моргает», если есть
    // закрытые глаза), поэтому лист из трёх картинок — тоже работающий лист.
    let sheet = null;
    const sheetSprite = new PIXI.Sprite(PIXI.Texture.EMPTY);
    const sheetUrl = (opts && opts.sheetUrl) || '/sprites/sheet.json';

    function loadSheetTexture(url) {
      return new Promise((resolve) => {
        if (PIXI.Assets && PIXI.Assets.load) {
          Promise.resolve(PIXI.Assets.load(url))
            .then((tex) => resolve(tex || null))
            .catch(() => resolve(null));
          return;
        }
        const img = new Image();
        img.onload = () => resolve(PIXI.Texture.from(img));
        img.onerror = () => resolve(null);
        img.src = url;
      });
    }

    function enterSheetMode() {
      body.visible = false;
      tailL.visible = false;
      tailR.visible = false;
      armL.visible = false;
      armR.visible = false;
      armUpWrap.visible = false;
      left.holder.visible = false;
      right.holder.visible = false;
      closedL.visible = false;
      closedR.visible = false;
      happyL.visible = false;
      happyR.visible = false;
      for (const key of Object.keys(brows)) brows[key].visible = false;
      mouthHolder.visible = false;
      blush.visible = false;
      // Зеркало шага влево — вокруг центра спрайта, а не левого края: тогда
      // кадр остаётся на месте, а не уезжает из окна.
      sprite.pivot.set(SPRITE_W / 2, 0);
      sprite.scale.x = anim.walking < 0 ? -1 : 1;
      sprite.addChild(sheetSprite);
      syncSheetFrame(true);
    }

    function pickSheetFrame() {
      const f = sheet.frames;
      const has = (n) => (f[n] ? n : null);
      let name = null;
      if (anim.waving > 0) name = has('wave');
      if (!name && anim.talkOpen) name = has('talk');
      if (!name && anim.blinking) name = has('blink') || has('sleep');
      if (!name && anim.walking !== 0) name = (anim.walkFrame ? has('walk2') : has('walk1')) || has('stand');
      if (!name) name = has(anim.emotion);            // happy / sad / think / sleep
      if (!name && anim.pose === 'sit') name = has('sit');
      if (!name) name = has('stand');
      if (!name) name = Object.keys(f)[0];
      return name;
    }

    function syncSheetFrame(force) {
      if (!sheet) return;
      const name = pickSheetFrame();
      if (name === sheet.frame && !force) return;
      const tex = sheet.frames[name];
      // Кадр ставится по ШИРИНЕ окна спрайта (ползунок «размер» — масштаб
      // корня), низ кадра — по полу спрайта, центр по центру: тогда персонаж
      // стоит на том же месте, что и нарисованный, а тень под ногами не
      // разъезжается.
      const sy = SPRITE_W / tex.width;
      sheetSprite.texture = tex;
      sheetSprite.scale.set(sy, sy);
      sheetSprite.position.set((SPRITE_W - tex.width * sy) / 2, SPRITE_H - tex.height * sy);
      sheetSprite.visible = true;
      sheet.frame = name;
    }

    function loadSheet() {
      return fetch(sheetUrl, { cache: 'no-store' })
        .then((res) => (res.ok ? res.json() : null))
        .then((manifest) => {
          if (!manifest || !manifest.frames) return false;
          const base = sheetUrl.slice(0, sheetUrl.lastIndexOf('/') + 1);
          const names = Object.keys(manifest.frames);
          return Promise.all(names.map((n) => loadSheetTexture(base + manifest.frames[n]))).then((texs) => {
            const frames = {};
            for (let i = 0; i < names.length; i++) {
              const t = texs[i];
              // Пустая битая картинка не должна становиться «кадром»:
              // иначе персонаж исчезнет, а человек решит, что сломался код.
              if (t && t.width > 0 && t.height > 0) frames[names[i]] = t;
            }
            if (!Object.keys(frames).length) return false;
            sheet = { frames: frames, frame: null };
            enterSheetMode();
            return true;
          });
        })
        .catch(() => false);
    }

    setEmotion('calm');

    // hitTest — попадание мыши в персонажа (координаты ОТ ЦЕНТРА спрайта на
    // экране). Именно по нему решается, тянуть окно или отдать клик рабочему
    // столу: окно прямоугольное, а персонаж — нет.
    // Координаты приходят экранные, а спрайт нарисован крупнее сетки (scale),
    // поэтому делим на масштаб: иначе при крупном размере хватался бы только
    // левый верхний угол фигуры.
    function hitTest(x, y) {
      const hw = (SPRITE_W * scale) / 2;
      const hh = (SPRITE_H * scale) / 2;
      return x >= -hw && x <= hw && y >= -hh && y <= hh;
    }

    // headRect — область головы в ЭКРАННЫХ координатах от центра спрайта. По ней
    // распознаётся поглаживание (js/pet.js): держать эти числа в двух файлах —
    // верный способ разойтись, когда персонаж перерисуется.
    function headRect() {
      // Сетка спрайта 40×56, её центр — (20, 28). Голова занимает примерно
      // x 10..30 и y 3..25 в пикселях сетки.
      const cx = GW / 2;
      const cy = GH / 2;
      return {
        x0: (10 - cx) * PIX * scale,
        x1: (30 - cx) * PIX * scale,
        y0: (3 - cy) * PIX * scale,
        y1: (25 - cy) * PIX * scale,
      };
    }

    // setScale — размер клетки на экране (ползунок «размер»). Меняется масштаб
    // всего спрайта, а не перерисовка: сетка остаётся той же, поэтому моргание,
    // пряди и сердечки работают без правок.
    function setScale(pixOnScreen) {
      const px = Math.max(MIN_PIX, Math.min(MAX_PIX, Math.round(pixOnScreen || DEFAULT_PIX)));
      scale = px / PIX;
      root.scale.set(scale);
      api.width = SPRITE_W * scale;
      api.height = SPRITE_H * scale;
      api.radius = api.height / 2;
      return px;
    }

    // setPose — «стоять» или «сидеть». Позу выбирает человек в меню; страница
    // решает по ней, куда ставить окно (см. js/main.js, посадка на панель задач).
    function setPose(next) {
      const pose = next === 'sit' ? 'sit' : 'stand';
      if (pose === anim.pose) return anim.pose;
      anim.pose = pose;
      repaintBody();
      return anim.pose;
    }

    // repaintBody — перерисовать тело по текущей позе и кадру шага.
    // В режиме листа тело — целый кадр, перерисовывать сетку нечего.
    function repaintBody() {
      if (sheet) { syncSheetFrame(true); return; }
      const idx = sprite.getChildIndex(body);
      sprite.removeChild(body);
      body.destroy();
      let g = bodyGrid(anim.pose, anim.walking !== 0 ? anim.walkFrame : -1);
      // Ось X листа шага ориентирована на движение (вперёд = направо),
      // поэтому шаги влево — тот же лист, зеркальный ПОСМЕНТНО.
      if (anim.walking < 0) g = flipX(g);
      body = paint(PIXI, g, PIX);
      sprite.addChildAt(body, idx >= 0 ? idx : 0);
    }

    // setWalk — она шагает: dir = направление (1 вправо, -1 влево, 0 стоп).
    // Кадр ноги меняется на месте (перерисовка одной детали, не всего спрайта).
    function setWalk(dir) {
      const d = dir > 0 ? 1 : dir < 0 ? -1 : 0;
      if (d === anim.walking) return anim.walking;
      anim.walking = d;
      anim.walkFrame = 0;
      anim.walkSwap = 0.26;
      // Лист смотрит вправо (как и лист шага), поэтому шаги влево — зеркало.
      if (sheet) sprite.scale.x = d < 0 ? -1 : 1;
      repaintBody();
      return anim.walking;
    }

    const api = {
      container: root,
      width: SPRITE_W,
      height: SPRITE_H,
      radius: SPRITE_H / 2,
      update: update,
      setEmotion: setEmotion,
      emotion: function () { return anim.emotion; },
      lookAt: lookAt,
      // lookPower — взгляд с силой слежения 0..1 (ползунок «следит за
      // курсором»); lookState — куда смотрят зрачки (самопроверка силы).
      lookPower: lookPower,
      lookState: function () { return { x: anim.lookX, y: anim.lookY }; },
      setBaseY: function (y) { anim.baseY = y; root.y = y; },
      hitTest: hitTest,
      headRect: headRect,
      setScale: setScale,
      scale: function () { return scale; },
      setPose: setPose,
      pose: function () { return anim.pose; },
      setMoodBand: setMoodBand,
      calmFace: calmFace,
      setWalk: setWalk,
      // walkLegTest — пиксельная проверка цикла шага (нужна самопроверке):
      // для каждого кадра возвращает колонки ног в зоне бёдер/голеней
      // (строки 50..53 — ниже юбки, выше туфель). У исправного шага в обоих
      // кадрах ровно ДВЕ группы колонок с зазором между ними.
      walkLegTest: function () {
        const out = [];
        for (let f = 0; f < 2; f++) {
          const g = bodyGrid('stand', f);
          const cols = [];
          for (let x = 8; x < 32; x++) {
            let has = false;
            for (let y = 50; y <= 53; y++) {
              if (g.px[y * GW + x] !== null) { has = true; break; }
            }
            if (has) cols.push(x);
          }
          const groups = [];
          for (const x of cols) {
            if (groups.length && x <= groups[groups.length - 1][1] + 1) {
              groups[groups.length - 1][1] = x;
            } else groups.push([x, x]);
          }
          out.push(groups);
        }
        return out;
      },
      walking: function () { return anim.walking; },
      say: say,
      wave: wave,
      // Микроживость (этап 9а): fidget — запустить движение руками
      // (для самопроверки), fidgetState — текущее ('stretch'|'scratch'|
      // 'squirm'|'palm'|null), fidgetDebug — видимость рук и наклон для
      // пиксельной проверки, fidgetArm — подставить таймер автозапуска.
      fidget: function (kind) { return startFidget(kind); },
      fidgetState: function () { return anim.fidget ? anim.fidget.kind : null; },
      fidgetArm: function (sec) { anim.fidgetIn = Number(sec) || 0; },
      fidgetDebug: function () {
        return {
          kind: anim.fidget ? anim.fidget.kind : null,
          armUp: armUpWrap.visible,
          armUpL: armUpLeftWrap.visible,
          armL: armL.visible,
          armR: armR.visible,
          rot: Math.round(sprite.rotation * 1000) / 1000,
          armRotL: Math.round(armL.rotation * 1000) / 1000,
        };
      },
      // setDance — танец под музыку: вкл/выкл и частота подскока из
      // измеренного детектором темпа (рад/с, в узких пределах: медленный
      // «плывёт», быстрый «трясёт»). dancing — состояние для самопроверки.
      setDance: function (on, w) {
        anim.dance = !!on;
        if (on) {
          anim.danceW = Math.max(3.5, Math.min(12, Number(w) || 6.3));
          anim.danceT = 0;
        } else if (!sheet) {
          armUpWrap.visible = false;
          armR.visible = true;
          armUpWrap.rotation = 0;
        }
      },
      dancing: function () { return anim.dance; },
      pet: pet,
      // reactToUserState (этап 9в): код тона сообщений человека
      // ("sad", "tired", "happy", "angry", "anxious" — коды internal/moodread,
      // этап 10) — лицо сочувствия после её ответа. Код приходит с сервера
      // (локальный детектор), содержимое сообщений аватар не читает.
      reactToUserState: function (label) {
        const faces = {
          sad: 'sad', tired: 'think', anxious: 'think',
          angry: 'calm', happy: 'happy',
          // Русские ярлыки — от сервера до этапа 10: страница бывает новее
          // бинарника на один прогон.
          'расстроился': 'sad', 'устал': 'think', 'тревожится': 'think',
          'злится': 'calm', 'рад': 'happy',
        };
        const f = faces[label];
        if (!f) return;
        anim.userReact = { face: f, applied: false, until: anim.t + 12 };
      },
      // Сколько сердечек сейчас летит — нужно самопроверке: глазами эффект
      // успевает растаять между двумя взглядами на окно.
      hearts: function () { return hearts.length; },
      zzz: function () { return zzzs.length; },
      waving: function () { return anim.waving > 0; },
      // Режим листа (этап 6): открытая проверка — на листе ли персонаж,
      // какой кадр сейчас показан и в какую сторону смотрит лист.
      usingSheet: function () { return !!sheet; },
      currentFrame: function () { return sheet ? sheet.frame : null; },
      mirrored: function () { return sheet ? sprite.scale.x < 0 : false; },
    };
    api.sheetReady = loadSheet();
    return api;
  }

  return {
    create: create,
    emotions: EMOTIONS,
    palette: C,
    minPix: MIN_PIX,
    maxPix: MAX_PIX,
    defaultPix: DEFAULT_PIX,
  };
})();
