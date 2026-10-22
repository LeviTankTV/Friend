// js/music.js — «реакция на звук» (из Desktop Mate: «танцует под музыку»).
//
// Граница приватности здесь принципиальнее всего в проекте. Системный микшер
// (НЕ микрофон) читает Go-помощник soundwatch.exe (WASAPI loopback): из
// звукового сигнала он наружу отдаёт РОВНО RMS-число — строку {"level":…}.
// Числа через оболочку приходят сюда (bridge.onSound), здесь из них считается
// «играет музыка» и танец. Ничего содержимого звука нигде не сохраняется и
// никуда не отправляется: сервер и модель не получают даже флаг «танцует» —
// движение нужно только аватару, он живёт на этой же странице.
//
// Почему не Chromium-захват (getDisplayMedia/getUserMedia desktop): на этой
// машине (масштаб экрана 125%) все пути Chromium-захвата микшера убивали
// рендерер («Terminating renderer for bad IPC message, reason 263») или
// отвечали NotSupported. Помощник processом — тот же приём, что focuswatch.
//
// «Играет музыка» = сигнал заметно выше шумового порога комнаты И держится
// несколько секунд. Детектор намеренно тупой: порог учится из самых тихих
// моментов, громко = уровень выше порога на фиксированную величину. Никакой
// «классификации жанров» — она не нужна, чтобы просто задвигаться.
//
// Её собственный голос — не музыка: main.js сообщает модулю «она говорит»
// (TTS играет через те же колонки и попадает в микшер). Такой сигнал модуль
// считает музыкой с ровным темпом — она танцует под свой голос ровно, как
// это выглядит у Desktop Mate.
(function (global) {
  'use strict';

  const TICK_MS = 100;      // период замера уровня
  const LOUD_ABOVE = 0.018; // насколько уровень должен быть выше порога комнаты
  const LOUD_START = 30;    // 3 с подряд громко — музыка «играет всерьёз»
  const LOUD_OFF_MS = 8000; // 8 с тишины — музыка кончилась
  const PEAK_MIN_MS = 140;  // быстрее этого биты не считаются (защита от дрожи)

  let opts = null;          // { setCapture, isSelf, onState }
  let timer = 0;
  let running = false;
  let feedFn = null;        // тестовый режим: уровень подаёт оболочка
  let lastLevel = 0;        // последнее RMS-число от soundwatch
  let level = 0;
  let floor = 0.02;         // шумовой порог комнаты (учится из тишины)
  let peaks = [];           // интервалы между пиками, мс (оценка темпа)
  let lastPeakAt = 0;
  let avgBoost = 0;         // средний «всплеск» пика по УРОВНЮ (не путать с
                            // интервалом: порог пика — уровень, а не мс)
  let loudStreak = 0;
  let lastLoudAt = 0;
  let lastMusicLoudAt = 0;  // последняя ВНЕШНЯЯ громкость (её голос — не музыка)
  let feedSelf = null;      // тест: режим «она говорит» подменяет isSelf
  let dancing = false;
  let bpm = 0;
  let error = '';

  function median(arr) {
    const s = arr.slice().sort((a, b) => a - b);
    return s[s.length >> 1];
  }

  function readLevel() {
    if (feedFn) return feedFn();
    return lastLevel;
  }

  // Оболочка зовёт это на каждое {"level":…} от помощника.
  function pushLevel(v) {
    if (typeof v === 'number' && isFinite(v)) lastLevel = v;
  }

  function tick() {
    const now = Date.now();
    let lv = Math.max(0, Math.min(1, readLevel()));
    // Голос в микшере неотличим от музыки по уровню, и по решению человека
    // её собственный голос танец НЕ продлевает: кончилась музыка — кончился и
    // танец, даже если она продолжает говорить реплики (баг «не перестаёт
    // танцевать, когда музыки уже нет»: каждый её ответ по чату обновлял
    // «последнюю громкость», и танец не кончался никогда). Поэтому в тишине
    // без музыки (последняя внешняя громкость была старше паузы) её речь для
    // детектора — тишина. Пока музыка играет, её голос в миксе остаётся как
    // есть: репликами под музыкой танец не обрывается.
    const selfNow = feedSelf !== null ? !!feedSelf : !!(opts.isSelf && opts.isSelf());
    if (selfNow && now - lastMusicLoudAt > LOUD_OFF_MS) lv = Math.min(lv, floor);
    level = lv;
    // Порог комнаты: вниз — быстро (услышала тишину), вверх — еле-еле
    // (не должен вырасти от самой музыки).
    if (lv < floor) floor = floor * 0.8 + lv * 0.2;
    else floor += (lv - floor) * 0.005;
    floor = Math.max(0.004, Math.min(0.12, floor));

    const loud = lv - floor > LOUD_ABOVE;
    if (loud) {
      loudStreak++;
      lastLoudAt = now;
      if (!selfNow) lastMusicLoudAt = now;  // её голос «музыкой» не считается
      // Пик (удар бита): всплеск заметно выше среднего пика, не чаще раза
      // в 140 мс. Интервалы между пиками дают темп.
      const boost = lv - floor;
      if (boost > Math.max(0.06, avgBoost * 0.6) && now - lastPeakAt >= PEAK_MIN_MS) {
        if (lastPeakAt) peaks.push(now - lastPeakAt);
        peaks = peaks.slice(-12);
        avgBoost = avgBoost ? avgBoost * 0.5 + boost * 0.5 : boost;
        lastPeakAt = now;
      }
    } else {
      loudStreak = 0;
    }

    if (peaks.length >= 4) {
      const iv = Math.max(333, median(peaks)); // темп не быстрее 180
      bpm = Math.round(60000 / iv);
    }

    if (!dancing && loudStreak >= LOUD_START) {
      dancing = true;
      opts.onState({ dancing: true, bpm: bpm });
      } else if (dancing && now - lastLoudAt > LOUD_OFF_MS) {
        dancing = false;
        peaks = []; lastPeakAt = 0; avgBoost = 0;
        opts.onState({ dancing: false, bpm: 0 });
      }
  }

  async function start() {
    if (running || feedFn) return running;
    error = '';
    // Просим оболочку поднять помощника (он печатает уровни, оболочка шлёт
    // их каналом 'sound:level' → pushLevel).
    try {
      const ok = await opts.setCapture(true);
      if (!ok) {
        error = 'помощник звука не поднялся';
        return false;
      }
      running = true;
      timer = setInterval(tick, TICK_MS);
      return true;
    } catch (e) {
      error = 'захват не открылся: ' + ((e && e.message) || String(e));
      running = false;
      return false;
    }
  }

  function stop() {
    const was = dancing;
    if (timer) { clearInterval(timer); timer = 0; }
    lastLevel = 0;
    if (running && opts && opts.setCapture) opts.setCapture(false);
    running = false;
    dancing = false; loudStreak = 0; peaks = []; lastPeakAt = 0; avgBoost = 0;
    lastMusicLoudAt = 0;
    if (was && opts && opts.onState) opts.onState({ dancing: false, bpm: 0 });
  }

  // Тестовый режим (для самопроверки оболочки): уровни подаёт оболочка,
  // никакого настоящего звука в проверке нет.
  let feedLevel = 0;
  function feedStart() {
    stop();
    feedFn = () => feedLevel;
    feedLevel = 0.01;
    feedSelf = null;
    floor = 0.02;
    timer = setInterval(tick, TICK_MS);
  }
  function feedLevelSet(v) { feedLevel = v; }
  // Тест «она говорит» без настоящего TTS: feedSelf(true) — модуль считает,
  // что в колонках её реплика (уровень высокий, музыки нет).
  function feedSelfSet(on) { feedSelf = !!on; }
  function feedStop() {
    if (timer) { clearInterval(timer); timer = 0; }
    feedFn = null;
    feedSelf = null;
    dancing = false; loudStreak = 0; peaks = []; lastPeakAt = 0; avgBoost = 0;
    if (opts && opts.onState) opts.onState({ dancing: false, bpm: 0 });
  }

  function state() {
    return {
      running: running,
      feed: !!feedFn,
      level: Math.round(level * 1000) / 1000,
      floor: Math.round(floor * 1000) / 1000,
      dancing: dancing,
      bpm: bpm,
      error: error,
    };
  }

  global.FriendMusic = {
    attach: (o) => { opts = o; return { start, stop, state, pushLevel, feedStart, feedLevelSet, feedSelfSet, feedStop }; },
  };
})(window);
