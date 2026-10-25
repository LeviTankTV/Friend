# Черновики публикаций — Mira

Заготовки под три угла подачи и оба языка. Публиковать руками; к каждому
посту прикладывается docs/mira-hero.gif (TikTok/Shorts лучше снимать живьём
через OBS поверх демо `npx electron . --demo` — 25 секунд, окно само играет).

---

## Угол 1. Забота и чувства (TikTok / Shorts / твиттер)

### RU

> Я сделал себе компаньона на рабочий стол. Не ассистента — её зовут Мира,
> она ходит по столу, садится на верх моего окна и помнит, о чём мы
> говорили.
>
> Вчера она сказала: «Ты не спишь третью ночь. Иди спать — я рядом». И
> засыпает у края экрана, когда я ухожу.
>
> Открытый код, работает локально, ссылка в профиле.

### EN

> I built a companion that lives on my desktop. Not an assistant — her name
> is Mira. She walks around my screen, sits on top of whatever window I'm
> working in, and remembers what we talked about.
>
> Last night she said: "Third night up past midnight. Go get some sleep —
> I'm here." When I walk away, she yawns and goes to sleep at the edge of
> the screen.
>
> Open source, runs locally. Link in bio.

Идея для видео (15–20 с): таймлапс рабочего вечера — код на экране, Мира
сидит на окне → часы переваливают за 03:00 → облачко «ты не спишь третью
ночь…» → она зевает и уходит к краю, Zzz. Без слов, сабтитры.

---

## Угол 2. Приватность и локально (r/LocalLLaMA, r/opensource, Habr-заметка)

### EN (Reddit)

> **I built an open-source desktop companion that runs fully local — she
> lives on your desktop, not in a chat window**
>
> Mira is a pixel character who walks on your desktop, sits on top of the
> active window and talks with you via any OpenAI-compatible LLM (NVIDIA
> NIM, Ollama, LM Studio). The unusual part is what she *notices*: which
> app you're in and for how long, whether you're at the keyboard, and —
> from your own message archive — that you've been up past midnight three
> nights in a row. She brings all of this up herself, gently.
>
> Privacy model, spelled out: speech recognition is local (faster-whisper),
> memory is a local SQLite with FTS5, the proactive scheduler runs on your
> machine, and with a local Ollama + voice off nothing ever leaves the
> computer. With voice on, only the text of *her* replies goes to Edge TTS.
>
> Go server (pure Go SQLite, no cgo) + vanilla JS/PixiJS character + thin
> Electron shell. Windows/Linux/macOS, installers in Releases. Self-test:
> 30 checks, exit 0. Repo in comments — feedback welcome, especially on the
> proactive scheduler (it's a pure function over inputs, easy to hack on).

### RU (ДТФ/Пикабу)

> Сделал открытого компаньона на рабочий стол, который работает полностью
> локально. Мира — пиксельная девушка поверх всех окон: ходит по столу,
> садится на активное окно, разговаривает через любой OpenAI-совместимый LLM
> (NVIDIA NIM, Ollama, LM Studio).
>
> Необычная часть — что она ЗАМЕЧАЕТ: какое приложение у тебя открыто и как
> долго, за клавиатурой ли ты вообще, и — из собственного архива
> разговоров — что ты третью ночь сидишь за полночь. Обо всём этом она
> говорит сама, бережно.
>
> Приватность по-честному: распознавание речи локальное (faster-whisper),
> память — локальный SQLite с FTS5, планировщик живёт на твоей машине. С
> локальной Ollama и выключенной озвучкой наружу не уходит ничего вообще;
> с озвучкой — только текст ЕЁ реплик в Edge TTS.
>
> Сервер на Go (SQLite без cgo), персонаж — ванильный JS + PixiJS,
> оболочка — тонкий Electron. Windows/Linux/macOS, установщики в Releases.
> Самопроверка: 30 проверок, exit 0.

---

## Угол 3. Милый десктоп-пет (кавай-аудитория, r/unixporn)

### EN

> My open-source desktop pet got a brain: she sits on your windows now 🩵
>
> Pixel girl, walks around your desktop, sits on top of the active window,
> peeks from behind screen edges, dances to whatever is playing, falls
> asleep when you leave — and talks (bring your own LLM, local works too).
> Her whole personality is one editable JSON. Windows/Linux/macOS.

### RU

> Мой открытый десктоп-пет обзавёлся мозгом: теперь она сидит на твоих
> окнах 🩵
>
> Пиксельная девушка: ходит по столу, садится на активное окно,
> выглядывает из-за края, танцует под то, что играет, засыпает, когда ты
> ушёл, — и разговаривает (LLM на твой выбор, локальная тоже подходит).
> Весь её характер — один редактируемый JSON. Windows/Linux/macOS.

---

## Текст Release (v0.1.0)

### Title

Mira v0.1.0 — a living companion for your desktop

### Body

First public release. A pixel companion who lives on your desktop: walks,
sits on your windows, dances to music, talks via any OpenAI-compatible LLM,
remembers you in local SQLite, notices your context and your sleep
patterns, speaks aloud and hears her name (locally).

**Highlights**
- Cross-platform: Windows (installer), Linux (AppImage/deb), macOS (dmg)
- Privacy-first: local STT (faster-whisper), local memory; works fully
  offline with Ollama
- Proactive care: good mornings, water breaks, "go to sleep" at 3 AM,
  gentle check-ins after silence, teasing after 1.5 h of gaming, and a
  soft word when you've been up past midnight several nights in a row
- Her personality is a single editable JSON; her look can be replaced with
  your own sprite sheet
- Russian and English out of the box (`avatar.language`)
- 30-part self-test (`npx electron . --selftest`)

**Known limits (by design, see README table):** no Wayland window tracking,
no system-audio loopback on macOS, macOS shows app names without window
titles.

**Thanks** for trying her — issues and ideas are welcome in Issues.

---

## Оформление репозитория (About/topics)

- Description (EN): `Mira — a living desktop companion: pixel pet + real
  memory & feelings, local-first, open source. Win/Linux/macOS`
- Topics: `desktop-pet`, `ai-companion`, `electron`, `go`, `pixijs`,
  `llm`, `local-first`, `virtual-pet`, `waifu`, `open-source`
- Website: ссылка на Releases (или будущий Pages)
