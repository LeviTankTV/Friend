// lang.js — словари строк страницы (этап 10а).
//
// Локализация без сборщиков и фреймворков — как весь фронтенд: плоские
// словари «ключ → строка/массив» в js/lang/ru.js и js/lang/en.js, доступ
// через friendLang.L('ключ'). Русский — база: ключа нет в текущем языке —
// берётся русский, нет и там — сам ключ (баг видно сразу, а не пустым
// текстом).
//
// Язык приходит с сервером в welcome (avatar.language, поле конфига).
// Страница собирается раньше welcome, поэтому компоненты, рисующие текст
// один раз (поле ввода, меню), после смены языка перерисовывают подписи
// сами (setLanguage у чата и меню).
window.friendLang = (function () {
  'use strict';

  var dicts = (window.FRIEND_LANG = window.FRIEND_LANG || {});
  var current = 'ru';

  function pick(key) {
    var d = dicts[current];
    if (d && Object.prototype.hasOwnProperty.call(d, key)) return d[key];
    d = dicts.ru;
    if (d && Object.prototype.hasOwnProperty.call(d, key)) return d[key];
    return undefined;
  }

  // L('ключ') — строка; L('ключ', {name: 'Мира'}) — с подстановкой {name}.
  function L(key, vars) {
    var v = pick(key);
    if (v === undefined) return key;
    if (typeof v === 'string' && vars) {
      Object.keys(vars).forEach(function (k) {
        v = v.split('{' + k + '}').join(String(vars[k]));
      });
    }
    return v;
  }

  return {
    L: L,
    // list — массив (пулы фраз); не массив или нет ключа — пусто.
    list: function (key) { var v = pick(key); return Array.isArray(v) ? v : []; },
    set: function (code) {
      if (!dicts[code]) return;
      current = code;
      document.documentElement.setAttribute('lang', code);
    },
    code: function () { return current; },
  };
})();
