// pet.js — поглаживание по голове.
//
// Задача: отличить НЕЖНОЕ поглаживание от перетаскивания и просто случайного
// проезда курсора. Признаки, по которым это делается:
//   • курсор ходит ИМЕННО по голове (область знает персонаж — avatar.headRect);
//   • движения быстрые (поглаживание — это движение, а не замирание на месте);
//   • они длятся не меньше двух секунд и идут подряд: если курсор ушёл с головы
//     или остановился больше чем на полсекунды, попытка начинается заново.
//
// Такая проверка нарочно «неторопливая»: реакция на случайный проезд мыши
// выглядела бы как персонаж, который радуется чему угодно, и перестала бы что-то
// значить.
window.FriendPet = (function () {
  'use strict';

  // Сколько нужно гладить по голове, чтобы она обрадовалась. 1,1 с — это
  // два естественных взмаха ладонью по волосам; прежние 2 с непрерывного
  // движения живой человек выдерживал редко (проверка это ловила: «водил
  // курсором по голове — ноль реакции»), а смысл поглаживания — два- три
  // спокойных движения, а не марафон.
  const NEED_MS = 1100;
  // Пауза в движении, после которой поглаживание считается прерванным.
  const BREAK_MS = 700;
  // Ниже этой скорости это не поглаживание, а «мышь стоит».
  const MIN_SPEED = 90;         // пикселей в секунду
  // Повторное поглаживание так скоро не считается новым.
  const COOLDOWN_MS = 1200;
  // Запас на короткий выход за край волос (350 мс): естественные взмахи идут
  // туда-сюда, и на развороте курсор на доли секунды съезжает с головы.
  // Раньше это было «глажу заново с нуля» — и поглаживание не накапливалось
  // никогда.
  const GRACE_MS = 350;

  function attach(opts) {
    const o = opts || {};
    const stage = o.target;         // по чему ловим события
    const view = o.view;            // canvas: по нему считаем координаты
    const avatar = o.avatar;
    const bridge = o.bridge;

    let last = null;        // последняя точка курсора (в координатах страницы)
    let lastAt = 0;
    let stroked = 0;        // сколько накоплено поглаживания, мс
    let lastPetAt = 0;
    let lastOnHeadAt = 0;   // когда в последний раз двигались ПО голове

    function inHead(clientX, clientY) {
      if (!avatar.headRect) return false;
      const rect = view.getBoundingClientRect();
      const head = avatar.headRect();
      const x = clientX - rect.left - avatar.container.x;
      const y = clientY - rect.top - avatar.container.y;
      return x >= head.x0 && x <= head.x1 && y >= head.y0 && y <= head.y1;
    }

    function onMove(ev) {
      // Пока окно таскают, это не поглаживание: там мышь «тянет» персонажа,
      // и радоваться этому незачем.
      if (o.isDragging && o.isDragging()) {
        last = null;
        stroked = 0;
        return;
      }
      const now = performance.now();
      const point = { x: ev.clientX, y: ev.clientY };

      if (!last) {
        last = point;
        lastAt = now;
        stroked = inHead(point.x, point.y) ? stroked : 0;
        return;
      }

      const dt = Math.max(1, now - lastAt);
      const dist = Math.hypot(point.x - last.x, point.y - last.y);
      const speed = (dist / dt) * 1000;

      if (dist < 1) {
        // Курсор стоит: если так больше BREAK_MS, попытка сгорает.
        if (now - lastAt > BREAK_MS) stroked = 0;
        last = point;
        lastAt = now;
        return;
      }

      const onHead = inHead(point.x, point.y);
      if (onHead && speed >= MIN_SPEED) {
        // Если с последнего движения по голове cursor гулял мимо дольше
        // GRACE_MS (человек отвёл мышь, ушёл в другую часть экрана),
        // попытка начинается заново. Короткий вылет за край волос на
        // развороте ладони — не разрыв: накопление продолжается.
        stroked = now - lastOnHeadAt > GRACE_MS ? dt : stroked + dt;
        lastOnHeadAt = now;
      }

      last = point;
      lastAt = now;

      if (stroked >= NEED_MS && now - lastPetAt >= COOLDOWN_MS) {
        lastPetAt = now;
        stroked = NEED_MS * 0.6;   // продолжаем гладить — сердечки не прекращаем
        if (avatar.pet) avatar.pet();
        if (o.onPet) o.onPet();
      }
    }

    function reset() {
      last = null;
      stroked = 0;
    }

    stage.addEventListener('pointermove', onMove);
    stage.addEventListener('pointerleave', reset);
    window.addEventListener('blur', reset);

    return {
      reset: reset,
      // Сколько накоплено — нужно самопроверке, чтобы проверить правило без
      // «подожди и посмотри».
      progress: function () { return stroked; },
      detach: function () {
        stage.removeEventListener('pointermove', onMove);
        stage.removeEventListener('pointerleave', reset);
        window.removeEventListener('blur', reset);
      },
    };
  }

  return { attach: attach, needMs: NEED_MS };
})();
