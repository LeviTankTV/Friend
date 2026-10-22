// js/idlewatch.js — «сон/уход после простоя» (этап «уход после простоя»).
//
// Долго никого нет — она засыпает (закрытые глаза и «Zzz»; сон — событие
// лица, полоска настроения его не перебивает). Не отсутствует совсем долго —
// «уходит»: переезжает к ближайшему краю экрана и спит там, выглядывая,
// когда курсор подходит (тот же механизм прятания у края, что в меню).
// Человек вернулся — оживает и возвращается на своё место.
//
// Откуда известно про простой: поле `idle` кадров помощника focuswatch
// (GetLastInputInfo — сколько секунд система не видела НИ ОДНОГО движения
// мыши/клавиатуры, включая движение по её окну) плюс собственные события
// «рядом что-то было»: мышь по персонажу, клик, открытие меню и чата, её
// голос из колонок, начавшийся танец под музыку. Всё считается на странице;
// наружу не уходит ничего.
//
// Пороги: 10 минут тишины — сон; 30 минут — уход к краю. Занятость
// (её держат, открыто меню, идёт разговор, она шагает, танцует, говорит)
// сон и уход запрещает; возвращение (idle упал ниже порога сна) будит и
// возвращает на прежнюю посадку — даже если в этот момент она занята.
(function (global) {
  'use strict';

  const TICK_MS = 5000;             // период решения «спать ли»
  const SLEEP_MS = 10 * 60 * 1000;  // 10 минут без человека — сон
  const LEAVE_MS = 30 * 60 * 1000;  // 30 минут — уход к краю

  let opts = null;                  // { busy, onSleep, onWake, onLeave, onReturn }
  let timer = 0;
  let phase = 'awake';              // awake | asleep | gone
  let lastLocal = Date.now();       // последний «рядом что-то было»
  let osIdle = 0;                   // idle из последнего кадра помощника, мс
  let osAt = Date.now();            // когда этот кадр пришёл
  let feedMs = null;                // режим самопроверки: idle задаёт оболочка

  function idleNow() {
    const now = Date.now();
    const os = (feedMs !== null ? feedMs : osIdle) + (feedMs !== null ? 0 : now - osAt);
    return Math.max(os, now - lastLocal);
  }

  // Кадр focus от помощника: система сообщает, сколько она не видела ввода.
  function setOsIdle(ms) {
    osIdle = Math.max(0, ms);
    osAt = Date.now();
  }

  // Любое «рядом что-то было»: мышь по ней, клик, чат, её голос, танец.
  function touch() { lastLocal = Date.now(); }

  function busy() {
    try { return !!(opts && opts.busy && opts.busy()); } catch (e) { return false; }
  }

  function sleep() {
    phase = 'asleep';
    if (opts && opts.onSleep) opts.onSleep();
  }

  function wake() {
    const gone = phase === 'gone';
    phase = 'awake';
    if (opts && opts.onWake) opts.onWake();
    if (gone && opts && opts.onReturn) opts.onReturn();
  }

  function tick() {
    const idle = idleNow();
    if (idle < SLEEP_MS) {
      // Человек рядом. Сон отменили — в том числе когда она занята:
      // вернулся — значит будить и возвращать на место.
      if (phase !== 'awake') wake();
      return;
    }
    if (busy()) return;             // заняли — не трогаем
    if (idle >= LEAVE_MS) {
      if (phase === 'awake') sleep();
      if (phase === 'asleep') {
        phase = 'gone';
        if (opts && opts.onLeave) opts.onLeave();
      }
      return;
    }
    if (phase === 'awake') sleep();
  }

  function start() {
    stop();
    timer = setInterval(tick, TICK_MS);
  }
  function stop() {
    if (timer) { clearInterval(timer); timer = 0; }
  }

  // Режим самопроверки: оболочка задаёт простой числом (мс) без ожидания
  // получаса; feed(null) — вернуть настоящий счёт.
  function feed(ms) {
    if (ms === null || ms === undefined) { feedMs = null; osAt = Date.now(); return; }
    feedMs = Math.max(0, ms);
  }

  function state() {
    return {
      phase: phase,
      idleSec: Math.round(idleNow() / 1000),
      feed: feedMs !== null,
    };
  }

  global.FriendIdle = {
    attach: (o) => { opts = o; start(); return { state, feed, touch, setOsIdle, start, stop }; },
  };
})(window);
