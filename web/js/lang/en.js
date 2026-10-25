// lang/en.js — английский словарь. Её фразы переведены живым языком, не
// буквально: это реплики персонажа, а не подписи интерфейса.
(function () {
  'use strict';
  var L = (window.FRIEND_LANG = window.FRIEND_LANG || {});
  L.en = {
    // index.html / main.js: каркас страницы
    'app.title': 'Mira',
    'app.stageAria': 'Companion',
    'hud.linkTitle': 'connection to server',
    'hud.hint': 'click — talk to her, drag — move her',

    // chat.js
    'chat.placeholder': 'write to her…',
    'chat.aria': 'message to the companion',
    'chat.sendTitle': 'Send (Enter)',
    'chat.micTitle': 'Speak aloud — transcribed locally into the field',
    'chat.micTitleStop': 'Stop and transcribe',
    'chat.you': 'you',
    'chat.errorWho': "didn't work",
    'chat.defaultName': 'Mira',
    'chat.errTimeout': 'No answer came. Check the connection dot in the corner of the window.',
    'chat.errNoConn': 'No connection to the server: the message was not sent. The dot in the corner shows the connection state.',
    'chat.errMic': 'Microphone did not open: {reason}.',
    'chat.micDenied': 'access denied',
    'chat.errEmptyRec': 'The recording came out empty — try again.',
    'chat.errStt': 'Transcription failed: {reason}.',
    'chat.sttNoAnswer': 'no response from the server',
    'chat.errNoHear': "I couldn't make it out — speak a little louder.",
    'chat.errReply': "Couldn't answer.",

    // menu.js
    'menu.title': 'companion',
    'menu.aria': 'companion menu',
    'menu.sizeLabel': 'size',
    'menu.sizeAria': 'companion size',
    'menu.followLabel': 'watches the cursor',
    'menu.followAria': 'cursor-following strength',
    'menu.groupSeat': 'place her',
    'seat.free.label': 'free',
    'seat.free.hint': 'stays wherever you drag her',
    'seat.taskbar.label': 'on the taskbar',
    'seat.taskbar.hint': 'sits on the bottom bar',
    'seat.window.label': 'on a window',
    'seat.window.hint': 'sits on top of the active window; when it changes, she moves to the new one',
    'seat.edge-left.label': 'at the left edge',
    'seat.edge-left.hint': 'peeks from behind the screen edge',
    'seat.edge-right.label': 'at the right edge',
    'seat.edge-right.hint': 'peeks from behind the screen edge',
    'toggle.keepTop': 'always on top',
    'toggle.voice': 'speaks aloud',
    'toggle.dance': 'dances to music',
    'toggle.wake': 'hears her name',
    'toggle.autostart': 'start on login',
    'action.talk': 'talk',
    'action.wave': 'wave',
    'action.home': 'home',
    'action.quit': 'quit',
    'menu.hint': 'right click — menu, drag — move, wheel — size',

    // main.js
    'wake.dotTitle': '{name} listens only for her name: audio is transcribed locally and never recorded',
    'wake.phrases': ['Yes! Right here.', 'Listening.', "I'm here!", 'Mhm, I hear you.'],
    'wake.awakePhrase': "Mm? Yes, I'm here.",
    'poke.phrases': ['Hm?', "I'm here.", 'Yes, right here.', 'Hm? What happened?', "It's nice that you notice me.", 'Mhm, listening.'],
    'seat.noWindow': "Nothing to sit on right now — there's no active window.",
    // Демо-сцены (--demo): записываются в hero-GIF, поэтому фразы — из словаря.
    'demo.hello': "Hi! I'm Mira.",
    'demo.care': 'Third night up past midnight. Go get some sleep — I am here.',
  };
})();
