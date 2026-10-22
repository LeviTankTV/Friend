// peek.js — как она выглядывает из-за края экрана.
//
// Зачем отдельный файл: у края экрана она не стоит на месте. Она прячется за
// кромку и время от времени выглядывает — так же ведут себя персонажи Desktop
// Mate и MateEngine: живут за границей экрана и «выходят» к человеку. Это
// отдельный РИТМ (паузы, плавный выезд, пауза на виду, плавный уход), и держать
// его в js/main.js значило бы править движение окна ради того, чтобы поменять
// длину паузы. Геометрию знает main.js (там окно и посадка), ритм — здесь.
//
// Наружу отдаётся ОДНО число: доля выхода, 0..1.
//   0 — она целиком за кромкой (за краем экрана),
//   1 — вышла и стоит на виду.
// Что это значит в пикселях, решает main.js: он знает ширину спрайта и то, на
// каком краю экрана она сидит.
window.FriendPeek = (function () {
  'use strict';

  // Времена. Выезд короче ухода: «выглянула» — это про любопытство, «спряталась»
  // — про покой. Паузы случайные: одинаковый ритм через минуту превращается в
  // метроном и начинает раздражать, а живой персонаж так себя не ведёт.
  const OUT_MS = 900;          // плавный выезд
  const IN_MS = 1200;          // плавный уход
  const POKE_OUT_MS = 380;     // отклик на мышь: она откликается быстрее
  const HIDDEN_MIN_MS = 4000;  // сколько сидит за краем
  const HIDDEN_MAX_MS = 9000;
  const SHOWN_MIN_MS = 2600;   // сколько стоит на виду
  const SHOWN_MAX_MS = 5200;

  function easeOut(p) { return 1 - Math.pow(1 - p, 3); }

  function easeInOut(p) {
    return p < 0.5 ? 4 * p * p * p : 1 - Math.pow(-2 * p + 2, 3) / 2;
  }

  function create(opts) {
    const o = opts || {};
    const rand = typeof o.rand === 'function' ? o.rand : Math.random;

    let dir = 0;            // -1 — прячется за ЛЕВЫМ краем, +1 — за правым, 0 — не у края
    let out = 1;            // доля выхода
    let phase = 'out';      // hidden | coming | out | leaving
    let t = 0;              // сколько миллисекунд тянется текущая фаза
    let wait = 0;           // сколько она в ней пробудет
    let from = 1;           // с какой доли начался текущий переезд
    let slideMs = OUT_MS;   // длительность текущего переезда

    function between(lo, hi) { return lo + rand() * (hi - lo); }
    const clamp01 = (v) => Math.max(0, Math.min(1, v));

    // holdOut спрашивает main.js: «её сейчас нельзя прятать?» (открыт разговор,
    // она отвечает). Без этого она уходила бы за край посреди собственной реплики.
    function hold() { return typeof o.holdOut === 'function' ? !!o.holdOut() : false; }

    // setDir — куда её посадили: -1 — за ЛЕВЫМ краем экрана, +1 — за правым,
    // 0 — не у края. Знак — это сторона кромки, за которой она прячется, и он же
    // задаёт направление движения спрайта (в main.js); поэтому здесь числа и
    // только они — строки «left»/«right» рядом с числами уже один раз разошлись
    // с вызовом и молча оставили её стоять на виду.
    function setDir(next) {
      const d = next === 'left' || next === -1 ? -1 : next === 'right' || next === 1 ? 1 : 0;
      if (d === dir) return dir;
      dir = d;
      t = 0;
      if (!d) {
        phase = 'out';
        from = out = 1;
        return dir;
      }
      // Посадили к краю: она прячется, но не сразу — сначала выглядывает
      // (см. poke ниже в main.js): человек только что выбрал «у края», и
      // персонаж, который мгновенно исчез, выглядит как поломка.
      phase = 'hidden';
      from = out = 0;
      wait = 0;
      return dir;
    }

    // poke — «покажись». Так реагируют на мышь рядом с кромкой, на реплику, на
    // поглаживание: у края экрана иначе её просто не достать.
    function poke() {
      if (!dir) return;
      if (phase === 'out') {
        // Уже на виду — просто продлеваем, а не начинаем заново: иначе она
        // дёргалась бы на каждое движение мыши.
        t = 0;
        wait = Math.max(wait, between(SHOWN_MIN_MS, SHOWN_MAX_MS));
        return;
      }
      if (phase === 'coming') return;
      phase = 'coming';
      from = out;
      t = 0;
      slideMs = POKE_OUT_MS;
    }

    // snap — поставить выход мгновенно. Нужно проверке оболочки: она должна
    // видеть обе крайние позы, не выжидая случайную паузу.
    function snap(v) {
      out = clamp01(v);
      from = out;
      t = 0;
      if (!dir) { phase = 'out'; out = 1; return out; }
      if (out >= 1) {
        phase = 'out';
        wait = between(SHOWN_MIN_MS, SHOWN_MAX_MS);
      } else {
        phase = 'hidden';
        wait = between(HIDDEN_MIN_MS, HIDDEN_MAX_MS);
      }
      return out;
    }

    // tick — кадр анимации. Возвращает долю выхода.
    function tick(dt) {
      if (!dir) { out = 1; return out; }
      const step = Number.isFinite(dt) && dt > 0 ? dt : 16;
      t += step;
      if (phase === 'hidden') {
        if (t >= wait) { phase = 'coming'; from = out; t = 0; slideMs = OUT_MS; }
      } else if (phase === 'coming') {
        const p = Math.min(1, t / slideMs);
        out = from + (1 - from) * easeOut(p);
        if (p >= 1) {
          out = 1;
          phase = 'out';
          t = 0;
          wait = between(SHOWN_MIN_MS, SHOWN_MAX_MS);
          // Вышла — иногда машет. Не каждый раз: постоянный жест — это уже
          // механика, а не «она тебя заметила».
          if (o.onWave && rand() < 0.3) o.onWave();
        }
      } else if (phase === 'out') {
        if (t >= wait && !hold()) { phase = 'leaving'; from = out; t = 0; }
      } else if (phase === 'leaving') {
        const p = Math.min(1, t / IN_MS);
        out = from * (1 - easeInOut(p));
        if (p >= 1) {
          out = 0;
          phase = 'hidden';
          t = 0;
          wait = between(HIDDEN_MIN_MS, HIDDEN_MAX_MS);
        }
      }
      return out;
    }

    return {
      setDir: setDir,
      poke: poke,
      snap: snap,
      tick: tick,
      dir: function () { return dir; },
      out: function () { return out; },
      phase: function () { return phase; },
      // Сколько пикселей ей осталось пройти до полного выхода — по этому числу
      // main.js двигает спрайт. Всё, что нужно знать о геометрии, — ширина
      // спрайта и то, на каком краю она сидит.
      hidden: function () { return out <= 0.001; },
    };
  }

  return { create: create, durations: { OUT_MS: OUT_MS, IN_MS: IN_MS, POKE_OUT_MS: POKE_OUT_MS } };
})();
