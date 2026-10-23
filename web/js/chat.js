// chat.js — разговор с персонажем: облачко реплик, окно ввода, «думает».
//
// Решения, которые здесь важны:
//   • окно ввода открывается КЛИКОМ по самому персонажу. Окно компаньона
//     прозрачное и висит поверх всех: постоянно торчащее поле ввода отнимало бы
//     у рабочего стола место и внимание. Разговор — по требованию;
//   • облачко живёт внутри окна персонажа, а не в отдельном окне: отдельное
//     пришлось бы двигать за персонажем и держать поверх него;
//   • реплика человека показывается сразу, до ответа сервера. Иначе набранный
//     текст исчезает и непонятно, ушёл он или нет;
//   • индикатор «думает» гасится по таймауту. Сервер тоже гасит его в любом
//     случае, но если канал лёг совсем, окно не должно остаться с «думает»
//     навсегда — это то состояние, из которого человек не знает выхода.
window.FriendChat = (function () {
  'use strict';

  // Сколько держится облачко. Оно часть окна поверх всех окон: вечно висящий
  // текст перекрывает человеку работу, поэтому реплика прячется сама.
  const HIDE_AFTER_MS = 16000;
  // Ошибку читают дольше, чем реплику: ей нужен таймаут побольше.
  const ERROR_HIDE_AFTER_MS = 30000;
  // Страховка на «думает» (у сервера свой дедлайн — см. hub.replyTimeout).
  const THINKING_TIMEOUT_MS = 60000;
  // Предел длины реплики — ровно как на сервере (maxSayRunes в hub.go). Разные
  // числа дали бы «отправил — и тишина» вместо понятного отказа.
  const MAX_LEN = 2000;

  function create(opts) {
    const o = opts || {};
    const net = o.net;
    const bridge = o.bridge;
    const avatar = o.avatar;

    // onActivity — «в разговоре что-то произошло». Нужно тому, кто сажает её к
    // краю экрана: там она прячется за кромку, и реплику (свою или её) надо
    // встретить уже на виду, а не из-за края (см. js/main.js, peek.poke).
    function activity() { if (o.onActivity) o.onActivity(); }

    // --- облачко ---------------------------------------------------------
    const bubble = document.createElement('div');
    bubble.className = 'bubble';
    bubble.hidden = true;

    const who = document.createElement('span');
    who.className = 'bubble-who';

    const dots = document.createElement('span');
    dots.className = 'bubble-dots';
    dots.hidden = true;
    for (let i = 0; i < 3; i++) dots.appendChild(document.createElement('i'));

    const text = document.createElement('span');
    text.className = 'bubble-text';

    bubble.appendChild(who);
    bubble.appendChild(dots);
    bubble.appendChild(text);

    // --- поле ввода ------------------------------------------------------
    const form = document.createElement('form');
    form.className = 'chat-input';
    form.hidden = true;

    const field = document.createElement('textarea');
    field.rows = 1;
    field.maxLength = MAX_LEN;
    field.placeholder = friendLang.L('chat.placeholder');
    field.setAttribute('aria-label', friendLang.L('chat.aria'));

    const submit = document.createElement('button');
    submit.type = 'submit';
    submit.textContent = '→';
    submit.title = friendLang.L('chat.sendTitle');

    // Микрофон (этап 7): появляется только когда сервер подтвердил, что
    // умеет расшифровывать (поле stt в welcome).
    const mic = document.createElement('button');
    mic.type = 'button';
    mic.className = 'chat-mic';
    mic.textContent = '🎙';
    mic.title = friendLang.L('chat.micTitle');
    mic.hidden = true;

    form.appendChild(field);
    form.appendChild(mic);
    form.appendChild(submit);

    document.body.appendChild(bubble);
    document.body.appendChild(form);

    // --- состояние -------------------------------------------------------
    let hideTimer = null;
    let thinkingTimer = null;
    let thinking = false;
    // Имя до welcome: язык страницы уже известен при сборке (словарь ru по
    // умолчанию), welcome его уточнит вместе с именем персоны.
    let chatName = friendLang.L('chat.defaultName');

    function setName(name) {
      if (name) chatName = name;
    }

    function hideBubble() {
      clearTimeout(hideTimer);
      hideTimer = null;
      bubble.hidden = true;
    }

    // showLine — показать одну строку: её реплику, слова человека или ошибку.
    // Стиль зависит от того, КТО это сказал: чужой текст в облачке выглядит как
    // её слова, если не отличается оформлением.
    function showLine(kind, message, proactive) {
      activity();
      clearTimeout(hideTimer);
      bubble.className = 'bubble ' + kind + (proactive ? ' proactive' : '');
      bubble.hidden = false;
      dots.hidden = true;
      text.hidden = false;
      who.textContent = kind === 'you' ? friendLang.L('chat.you') : (kind === 'error' ? friendLang.L('chat.errorWho') : chatName);
      text.textContent = message;
      hideTimer = setTimeout(hideBubble, kind === 'error' ? ERROR_HIDE_AFTER_MS : HIDE_AFTER_MS);
    }

    function showThinking() {
      clearTimeout(hideTimer);
      bubble.className = 'bubble thinking';
      bubble.hidden = false;
      who.textContent = '';
      text.hidden = true;
      dots.hidden = false;
    }

    function setThinking(on) {
      on = !!on;
      if (on === thinking) return;
      if (on) activity();
      thinking = on;
      clearTimeout(thinkingTimer);
      thinkingTimer = null;
      if (on) {
        showThinking();
        if (avatar) avatar.setEmotion('think');
        thinkingTimer = setTimeout(() => {
          setThinking(false);
          showLine('error', friendLang.L('chat.errTimeout'));
        }, THINKING_TIMEOUT_MS);
        return;
      }
      if (avatar) avatar.setEmotion(avatar.calmFace());
    }

    // --- отправка --------------------------------------------------------
    function sendText() {
      const value = field.value.trim();
      if (!value) return;
      if (!net.send({ type: 'say', text: value })) {
        showLine('error', friendLang.L('chat.errNoConn'));
        return;
      }
      showLine('you', value);
      field.value = '';
      autosize();
    }

    function autosize() {
      field.style.height = 'auto';
      field.style.height = Math.min(72, Math.max(24, field.scrollHeight)) + 'px';
    }

    form.addEventListener('submit', (ev) => {
      ev.preventDefault();
      sendText();
    });

    field.addEventListener('input', autosize);

    field.addEventListener('keydown', (ev) => {
      if (ev.key === 'Enter' && !ev.shiftKey) {
        ev.preventDefault();
        sendText();
        return;
      }
      if (ev.key === 'Escape') {
        ev.preventDefault();
        close();
      }
    });

    // --- микрофон (этап 7: голосовой ввод) --------------------------------
    // Запись никуда, кроме текста, не превращается сама: страница пересобирает
    // её в WAV 16 кГц mono и отправляет на /stt, где расшифровывает ЛОКАЛЬНАЯ
    // модель. Слово человека остаётся словом в поле ввода — серверу mic-поток
    // не показывается, в память не пишется, в облако не уходит.
    let mediaStream = null;
    let recorder = null;
    let chunks = null;
    let micTimer = null;
    let micDiscard = false;

    function micRecording() { return !!(recorder && recorder.state === 'recording'); }

    function micLook(on) {
      mic.classList.toggle('recording', on);
      mic.textContent = on ? '⏹' : '🎙';
      mic.title = on ? friendLang.L('chat.micTitleStop') : friendLang.L('chat.micTitle');
    }

    function pickMime() {
      const cands = ['audio/webm;codecs=opus', 'audio/webm', 'audio/mp4'];
      for (const m of cands) {
        if (window.MediaRecorder && MediaRecorder.isTypeSupported(m)) return m;
      }
      return '';
    }

    async function startMic() {
      if (micRecording()) return;
      micDiscard = false;
      try {
        mediaStream = await navigator.mediaDevices.getUserMedia({
          audio: { echoCancellation: true, noiseSuppression: true },
        });
      } catch (e) {
        showLine('error', friendLang.L('chat.errMic', { reason: e && e.name ? e.name : friendLang.L('chat.micDenied') }));
        return;
      }
      chunks = [];
      const mime = pickMime();
      recorder = mime ? new MediaRecorder(mediaStream, { mimeType: mime }) : new MediaRecorder(mediaStream);
      recorder.ondataavailable = (ev) => { if (ev.data && ev.data.size) chunks.push(ev.data); };
      recorder.onstop = finishRecording;
      recorder.start(250);
      micLook(true);
      // Потолок записи — минута: longer речи компаньону редки, а забытый
      // открытым микрофон — худший из возможных сценариев.
      micTimer = setTimeout(() => { if (micRecording()) recorder.stop(); }, 60000);
    }

    async function finishRecording() {
      clearTimeout(micTimer);
      micLook(false);
      if (mediaStream) {
        mediaStream.getTracks().forEach((t) => t.stop());
        mediaStream = null;
      }
      recorder = null;
      const data = chunks || [];
      chunks = null;
      const discard = micDiscard;
      micDiscard = false;
      if (discard) return;
      if (!data.length) { showLine('error', friendLang.L('chat.errEmptyRec')); return; }
      const blob = new Blob(data, { type: 'audio/webm' });
      let text = '';
      try {
        text = await o.transcribe(blob);
      } catch (e) {
        showLine('error', friendLang.L('chat.errStt', { reason: e && e.message ? e.message : friendLang.L('chat.sttNoAnswer') }));
        return;
      }
      if (!text) { showLine('error', friendLang.L('chat.errNoHear')); return; }
      // Слово человека в поле, а не сразу в эфир: набранный текст человек
      // читает глазами, и расшифрованное слово должно появиться в том же месте —
      // иначе непонятно, ушло оно или её надо отправить.
      field.value = field.value ? field.value + ' ' + text : text;
      autosize();
      field.focus();
    }

    mic.addEventListener('click', () => {
      if (micRecording()) { recorder.stop(); return; }
      startMic();
    });

    // setVoiceInput — сервер подтвердил расшифровку (stt в welcome) или снял
    // её; с выключением незавершённая запись честно выбрасывается.
    function setVoiceInput(on) {
      mic.hidden = !on;
      if (!on && micRecording()) {
        micDiscard = true;
        recorder.stop();
      }
    }

    // --- открыть/закрыть ввод --------------------------------------------
    function isOpen() { return !form.hidden; }

    function open() {
      if (isOpen()) {
        field.focus();
        return;
      }
      form.hidden = false;
      document.body.classList.add('chat-open');
      // Окно висит поверх всех и может быть не в фокусе (человек работает в
      // игре под ним): без явного фокуса набранные буквы уехали бы туда.
      if (bridge && bridge.focus) bridge.focus();
      field.focus();
      autosize();
    }

    function close() {
      if (!isOpen()) return;
      form.hidden = true;
      document.body.classList.remove('chat-open');
      field.blur();
      if (avatar && !thinking) avatar.setEmotion(avatar.calmFace());
    }

    function toggle() {
      if (isOpen()) close();
      else open();
    }

    // --- канал -----------------------------------------------------------
    if (net && net.on) {
      net.on('thinking', (msg) => setThinking(msg && msg.on));

      net.on('reply', (msg) => {
        setThinking(false);
        const proactive = !!(msg && msg.proactive);
        showLine('her', (msg && msg.text) || '', proactive);
        // Она ГОВОРИТ: рот открывается, пока идёт реплика. Это не украшение —
        // без этого ответ появляется текстом, а персонаж стоит с закрытым ртом,
        // и связь «она это сказала» теряется.
        if (avatar && avatar.say) avatar.say((msg && msg.text) || '');
        else if (avatar) avatar.setEmotion('happy');
        // Заговорила сама — коротко машет рукой: человек должен заметить, что
        // реплика от неё, а не ответ на то, что он набрал в поле.
        if (proactive && avatar && avatar.wave) avatar.wave();
        // Озвучка (этап 7): её собственная реплика — единственное, что
        // отправляется в облако TTS. Слова человека (kind 'you') не озвучиваем.
        if (o.speak) o.speak((msg && msg.text) || '');
        // «Zzz»-повод: она засыпает прямо во время фразы. say() её не будит
        // (рот открывается, глаза остаются закрытыми), а лицо после речи
        // должно вернуться не к «calm», а к сну.
        const emotion = msg && msg.emotion;
        if (emotion === 'sleep' && avatar) avatar.setEmotion('sleep');
        // Эмпатия (этап 9в): сервер присылает ярлык тона его сообщений
        // («расстроился», «устал», «рад»…) — лицо сочувствия применится,
        // когда она договорила. Пусто — нейтрально, не трогаем.
        const us = msg && msg.user_state;
        if (us && avatar && avatar.reactToUserState) avatar.reactToUserState(us);
        // Через несколько секунд лицо снова спокойное: постоянная улыбка — это
        // уже не эмоция, а маска. Спящую не будим: она остаётся с закрытыми
        // глазами, пока её не разбудит набор текста (setThinking).
        setTimeout(() => {
          if (!thinking && avatar && avatar.emotion() !== 'sleep') avatar.setEmotion(avatar.calmFace());
        }, 6000);
      });

      net.on('reply_error', (msg) => {
        setThinking(false);
        showLine('error', (msg && msg.error) || friendLang.L('chat.errReply'));
      });
    }

    // showHistory — последняя реплика из истории сервера. Сервер помнит
    // разговор, пока жив; перезагрузка страницы не должна выглядеть как «она
    // меня забыла». Показываем ровно одну строку: в облачке живёт одна.
    function showHistory(list) {
      if (!list || !list.length) return;
      const last = list[list.length - 1];
      showLine(last.role === 'user' ? 'you' : 'her', last.text || '');
    }

    // setLanguage — после welcome с другим языком: подписи, которые рисуются
    // один раз при сборке, обновляются на месте (сами сообщения ошибок уже
    // берутся из словаря в момент показа).
    function setLanguage() {
      field.placeholder = friendLang.L('chat.placeholder');
      field.setAttribute('aria-label', friendLang.L('chat.aria'));
      submit.title = friendLang.L('chat.sendTitle');
      micLook(false);
    }

    return {
      toggle: toggle,
      open: open,
      close: close,
      isOpen: isOpen,
      // isThinking — «она сейчас отвечает». По этому признаку страница решает,
      // можно ли прятать её за край экрана (см. js/main.js, holdOut).
      isThinking: function () { return thinking; },
      setName: setName,
      setLanguage: setLanguage,
      showLine: showLine,
      showHistory: showHistory,
      setThinking: setThinking,
      hideBubble: hideBubble,
      // микрофон (этап 7): вкл/выкл кнопки по флагу сервера и её состояние.
      setVoiceInput: setVoiceInput,
      micVisible: function () { return !mic.hidden; },
      micRecording: micRecording,
    };
  }

  return { create: create };
})();
