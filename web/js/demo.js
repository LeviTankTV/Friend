// demo.js — демо-режим для оболочки Tauri (--demo).
//
// Сцены те же, что у Electron-оболочки (приветствие, прогулка, панель задач,
// поглаживание, танец, забота и сон), но кадры компонует СТРАНИЦА:
// capturePage в Tauri нет, зато канвас персонажа — здесь, а облачко читается
// из DOM и дорисовывается поверх. Сердечки рисуются в самом канвасе
// (js/avatar.js) и попадают в кадр сами. Готовые RGBA-кадры страница шлёт
// оболочке (bridge.demoFrame), оболочка собирает GIF (крейт gif) и пишет
// docs/mira-hero.gif. Фон — тёмный: GIF с прозрачностью не нужен, а на
// GitHub тёмный блок выглядит как скриншот.
window.FriendDemo = (function () {
  'use strict';

  const W = 320;             // ширина кадра GIF (как у Электрона: resize до 320)
  const FRAME_MS = 160;      // шаг кадров: 6.25 кадра в секунду

  function b64FromBytes(bytes) {
    let bin = '';
    const CH = 8192;
    for (let i = 0; i < bytes.length; i += CH) {
      bin += String.fromCharCode.apply(null, bytes.subarray(i, i + CH));
    }
    return btoa(bin);
  }

  // composeFrame — один кадр: тёмный фон + канвас + облачко из DOM.
  function composeFrame(scale) {
    const fa = window.friendApp;
    const view = fa.app.view;
    const h = Math.round(view.height * scale);
    const c = document.createElement('canvas');
    c.width = W;
    c.height = h;
    const ctx = c.getContext('2d');
    ctx.fillStyle = '#0d1117';
    ctx.fillRect(0, 0, W, h);
    ctx.drawImage(view, 0, 0, W, h);

    // Облачко: фактический прямоугольник DOM и его текст. Рисуем тёмную
    // плашку с рамкой — как стиль .bubble (см. overlay.css).
    const bubble = document.querySelector('.bubble');
    if (bubble && !bubble.hidden) {
      const r = bubble.getBoundingClientRect();
      const bx = Math.round(r.left * scale);
      const by = Math.round(r.top * scale);
      const bw = Math.max(20, Math.round(r.width * scale));
      const bh = Math.max(16, Math.round(r.height * scale));
      ctx.fillStyle = 'rgba(18, 22, 34, 0.95)';
      ctx.strokeStyle = 'rgba(169, 201, 255, 0.22)';
      roundRect(ctx, bx, by, bw, bh, 8 * scale);
      ctx.fill();
      ctx.stroke();
      ctx.fillStyle = '#eef1f8';
      ctx.font = Math.max(9, Math.round(11.5 * scale)) + 'px sans-serif';
      const text = (bubble.querySelector('.bubble-text') || {}).textContent || '';
      wrapText(ctx, text, bx + 8 * scale, by + 16 * scale, bw - 14 * scale, 14 * scale);
    }
    return c;
  }

  function roundRect(ctx, x, y, w, h, r) {
    ctx.beginPath();
    ctx.moveTo(x + r, y);
    ctx.arcTo(x + w, y, x + w, y + h, r);
    ctx.arcTo(x + w, y + h, x, y + h, r);
    ctx.arcTo(x, y + h, x, y, r);
    ctx.arcTo(x, y, x + w, y, r);
    ctx.closePath();
  }

  function wrapText(ctx, text, x, y, maxW, lineH) {
    const words = String(text).split(/\s+/);
    let line = '';
    for (const word of words) {
      const probe = line ? line + ' ' + word : word;
      if (ctx.measureText(probe).width > maxW && line) {
        ctx.fillText(line, x, y);
        line = word;
        y += lineH;
      } else {
        line = probe;
      }
    }
    if (line) ctx.fillText(line, x, y);
  }

  async function run() {
    const fa = window.friendApp;
    const av = fa.avatar;
    const L = window.friendLang;
    const t = (ms) => new Promise((r) => setTimeout(r, ms));

    // Рамка кадра: тёмный фон вместо прозрачности, подсказку прячем.
    document.documentElement.style.background = '#0d1117';
    document.body.style.background = '#0d1117';
    const hint = document.getElementById('hint');
    if (hint) hint.style.display = 'none';
    // Детерминированный герой: размер 5 (настройки человека не должны
    // менять вид README) и пустое облачко — welcome реплеит ИСТОРИЮ
    // разговоров, и без этого в первом кадре висит чужая реплика
    // (находка первого прогона: «Запомнила: кота зовут Барсик-тест»).
    // Пауза длинная: окно обязано доехать до 248×375 до первого кадра,
    // иначе герой снимается в переходном размере (находка прогона).
    fa.applySize(5);
    fa.chat.hideBubble();
    await t(2000);

    let capturing = true;
    const capture = (async () => {
      while (capturing) {
        try {
          const view = fa.app.view;
          const scale = W / view.width;
          const c = composeFrame(scale);
          const d = c.getContext('2d').getImageData(0, 0, W, c.height).data;
          window.FriendBridge.demoFrame(b64FromBytes(d), W, c.height);
        } catch (_e) { /* кадр не вышел — пропускаем */ }
        await t(FRAME_MS);
      }
    })();

    try {
      // 1. Приветствие: радость, взмах, реплика.
      av.setEmotion('happy');
      if (av.wave) av.wave();
      fa.chat.showLine('her', L.L('demo.hello'));
      if (av.say) av.say(L.L('demo.hello'));
      await t(2300);
      fa.chat.hideBubble();
      // 2. Прогулка: кадры ног и покачивание.
      fa.startWalk(1, Date.now() + 2500);
      await t(2500);
      fa.stopWalk('демо');
      await t(400);
      fa.chat.hideBubble();
      // 3. Посадка на панель задач и потягивание.
      fa.applySeat('taskbar');
      await t(900);
      if (av.fidgetNow) av.fidgetNow('stretch');
      await t(2600);
      fa.chat.hideBubble();
      // 4. Поглаживание: синтетические движения по волосам — сердечки.
      const stage = document.getElementById('stage');
      const view = fa.app.view.getBoundingClientRect();
      const head = av.headRect();
      const hx = view.left + av.container.x + (head.x0 + head.x1) / 2;
      const hy = view.top + av.container.y + (head.y0 + head.y1) / 2;
      for (let i = 0; i < 160; i++) {
        stage.dispatchEvent(new PointerEvent('pointermove', {
          bubbles: true,
          clientX: hx + Math.sin(i / 3) * 34,
          clientY: hy + Math.cos(i / 4) * 10,
        }));
        await t(16);
      }
      await t(1400);
      fa.chat.hideBubble();
      // 5. Танец: feed-уровни ~150 ударов в минуту.
      const m = fa.music;
      if (m && m.feedStart) {
        m.feedStart();
        if (m.feedSelfSet) m.feedSelfSet(false);
        const beat = 60000 / 150 / 2;
        for (let i = 0; i < 14; i++) { m.feedLevelSet(i % 2 ? 0.006 : 0.05); await t(beat); }
        m.feedStop();
      } else {
        await t(1300);
      }
      await t(1200);
      // 6. Забота и сон: реплика, зевок, глаза закрылись, «Zzz».
      const care = L.L('demo.care');
      fa.chat.showLine('her', care);
      if (av.say) av.say(care);
      await t(2800);
      if (av.yawn) av.yawn();
      await t(1700);
      av.setEmotion('sleep');
      await t(2600);
    } catch (e) {
      console.error('[friend] demo: сцены упали:', e.message);
    }

    await t(900);
    capturing = false;
    await capture;
    window.FriendBridge.demoDone(true);
  }

  return {
    run: run,
    armed: function () { return window.__FRIEND_DEMO__ === true || window.__FRIEND_DEMO__ === '1'; },
  };
})();

// Запуск — когда страница собрана и оболочка попросила демо.
if (window.FriendDemo.armed()) {
  const arm = () => {
    if (window.friendApp && window.friendApp.avatar && window.FriendBridge.demoFrame) {
      window.FriendDemo.run();
    } else {
      setTimeout(arm, 250);
    }
  };
  setTimeout(arm, 250);
}
