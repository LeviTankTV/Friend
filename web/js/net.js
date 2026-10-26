// net.js — канал к Go-серверу компаньона.
//
// Контракт кадра — серверный (см. server/internal/server/protocol.go):
//   → hello {client, version}
//   ← welcome {protocol, state:{position}, avatar:{...}, history:[...]}
//   → position {x, y}          ← state {position, saved}
//   → ping                     ← pong
//   → say {text}               ← thinking {on} → reply {text, at} | reply_error {error}
//
// Переподключение с нарастающей паузой: сервер можно перезапустить, не трогая
// окно персонажа (он «моргнёт» индикатором и вернётся).
window.FriendNet = (function () {
  'use strict';

  const PROTOCOL = 1;
  const state = {
    status: 'off',     // off | connecting | on
    welcome: null,     // последний welcome (состояние и персонаж)
    attempts: 0,
    lastPong: 0,
  };
  const handlers = {
    status: [], welcome: [], state: [], error: [],
    // Разговор (этап 2): «думает», её реплика и понятный отказ вместо ответа.
    thinking: [], reply: [], reply_error: [],
    // Wake word (этап 7): в речевом сегменте прозвучало её имя.
    wake: [],
    // Смена языка (этап 13): сервер рассылает lang всем окнам.
    lang: [],
  };
  let ws = null;
  let retryTimer = null;
  let pingTimer = null;

  function emit(kind, payload) {
    for (const fn of handlers[kind]) {
      try { fn(payload); } catch (e) { console.error('обработчик', kind, e); }
    }
  }

  function setStatus(s) {
    if (state.status === s) return;
    state.status = s;
    emit('status', s);
  }

  function wsURL() {
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    // Порт тот же, с которого отдана страница: сервер компаньона отдаёт и
    // фронтенд, и /ws, поэтому «где страница — там и канал».
    return proto + '//' + location.host + '/ws';
  }

  function connect() {
    clearTimeout(retryTimer);
    setStatus('connecting');
    let sock;
    try {
      sock = new WebSocket(wsURL());
    } catch (e) {
      scheduleRetry();
      return;
    }
    ws = sock;

    sock.onopen = () => {
      state.attempts = 0;
      setStatus('on');
      send({ type: 'hello', client: window.FriendBridge ? window.FriendBridge.mode : 'web', version: PROTOCOL });
      startPing();
    };

    sock.onmessage = (ev) => {
      let msg = null;
      try { msg = JSON.parse(ev.data); } catch (e) { return; }
      switch (msg.type) {
        case 'welcome':
          state.welcome = msg;
          emit('welcome', msg);
          break;
        case 'state':
          emit('state', msg);
          break;
        case 'thinking':
          emit('thinking', msg);
          break;
        case 'reply':
          emit('reply', msg);
          break;
        case 'reply_error':
          // Ошибка ответа — не ошибка канала: связь жива, ответить не вышло.
          // Поэтому отдельное событие: окно показывает её как сообщение, а не
          // как «сервер сломался».
          emit('reply_error', msg);
          break;
        case 'wake':
          // Прозвучало её имя (этап 7) — окно будит её и отвечает на имя.
          emit('wake', msg);
          break;
        case 'lang':
          // Язык поменялся (этап 13): broadcast всем окнам.
          emit('lang', msg);
          break;
        case 'pong':
          state.lastPong = Date.now();
          break;
        case 'error':
          console.warn('сервер ответил ошибкой:', msg.error);
          emit('error', msg);
          break;
        default:
          break;
      }
    };

    sock.onclose = () => {
      stopPing();
      setStatus('off');
      scheduleRetry();
    };
    sock.onerror = () => { /* onclose придёт следом и всё решит */ };
  }

  function scheduleRetry() {
    state.attempts++;
    // 0.5s, 1s, 2s, 4s … не больше 10s: персонаж должен вернуться быстро.
    const delay = Math.min(10000, 500 * Math.pow(2, Math.min(state.attempts - 1, 5)));
    clearTimeout(retryTimer);
    retryTimer = setTimeout(connect, delay);
  }

  function startPing() {
    stopPing();
    pingTimer = setInterval(() => send({ type: 'ping' }), 20000);
  }

  function stopPing() {
    clearInterval(pingTimer);
    pingTimer = null;
  }

  function send(obj) {
    if (!ws || ws.readyState !== WebSocket.OPEN) return false;
    ws.send(JSON.stringify(obj));
    return true;
  }

  return {
    state: state,
    connect: connect,
    send: send,
    // Отправить позицию окна. Если канал лёг, значение потеряется — поэтому
    // main.js зовёт это ещё и при восстановлении связи (см. flush).
    sendPosition: function (x, y) { return send({ type: 'position', x: Math.round(x), y: Math.round(y) }); },
    on: function (kind, fn) {
      if (handlers[kind]) handlers[kind].push(fn);
      return this;
    },
  };
})();
