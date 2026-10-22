package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"friend/internal/chat"
	"friend/internal/config"
	"friend/internal/focus"
	"friend/internal/llm"
	"friend/internal/mood"
	"friend/internal/moodread"
	"friend/internal/state"
)

const (
	// writeWait — сколько ждём на запись одного кадра.
	writeWait = 10 * time.Second
	// pongWait — столько ждём pong на наш ping; больше — считаем клиента мёртвым.
	pongWait = 60 * time.Second
	// pingPeriod — период наших ping. Должен быть меньше pongWait, иначе будем
	// закрывать живые соединения.
	pingPeriod = 25 * time.Second
	// maxMessageSize — предел входящего кадра: клиент шлёт hello/position,
	// мегабайты сюда не приходят, а память на разбор тратится на каждого.
	maxMessageSize = 8 << 10
	// sendQueue — буфер отправки. Переполнение означает, что клиент не читает;
	// такой клиент отключается, а не копит кадры (иначе утечка на сервере).
	sendQueue = 32
	// chatQueue — сколько реплик человека ждут ответа. Больше трёх незачем:
	// человек не печатает быстрее, чем модель отвечает, а очередь из реплик
	// только растянула бы ожидание и запутала историю.
	chatQueue = 3
	// maxSayRunes — предел длины реплики. Длинный текст — это уже не разговор
	// с компаньоном, а вставленный документ: он жжёт токены и не влезает в
	// облачко.
	maxSayRunes = 2000
	// replyMargin — запас к таймауту модели: дедлайн на всю операцию, а не
	// только на HTTP-запрос (сборка запроса, ожидание свободного потока).
	replyMargin = 5 * time.Second
)

// Responder — тот, кто отвечает на реплики человека. В рабочем запуске это
// companion.Companion, в тестах — подделка: проверять очередь канала на живом
// API нельзя (он медленный и требует ключа).
//
// History отдаётся окну при подключении: пока сервер жив, разговор не должен
// выглядеть начатым с нуля после перезагрузки страницы.
type Responder interface {
	// userState — ярлык настроения человека по последним репликам
	// (этап 9в, локальный детектор moodread; "" — нейтрально).
	Answer(ctx context.Context, text, userState string) (string, error)
	Ready() bool
	Status() string
	Timeout() time.Duration
	History() []chat.Message
}

// Hub держит подключённых клиентов (окно компаньона) и состояние.
type Hub struct {
	log       *slog.Logger
	store     *state.Store
	avatar    config.Avatar
	responder Responder
	// keyHint — что сказать человеку, если ключа нет (пути к файлу ключей
	// знает только запускающий код, поэтому текст приходит снаружи).
	keyHint string
	// focus — трекер активного окна (focuswatch → страница → сервер). Без него
	// компаньон живёт без «понимания экрана»: это не поломка, а отсутствие
	// функции, поэтому молча работать без трекера — норма (focuswatch только на Windows).
	focus *focus.Tracker
	// memory — долгая память (этап 3): команда «запомни:». Nil — память не
	// подключена, и «запомни:» честно объясняет это человеку.
	memory MemoryStore
	// moodEvents — канал внутреннего состояния (этап 5): реплика человека —
	// событие для модели настроения. Буферный и НЕ блокирующий: хаб не ждёт
	// цикла состояния, переполнение — просто пропущенное событие.
	moodEvents chan<- mood.Event
	// voice — доступность озвучки (этап 7) для welcome: фронтенд по ней
	// решает, просить ли /tts. Nil — озвучки нет.
	voice *VoiceInfo
	// stt — доступность голосового ввода (этап 7) для welcome: по ней
	// фронтенд показывает кнопку микрофона. Nil — ввода нет.
	stt *STTInfo
	// wake — доступность wake word (этап 7) для welcome: по ней фронтенд
	// разрешает фоновому слушателю работать (и тот требует отдельного
	// переключателя в меню). Nil — функция выключена.
	wake *WakeInfo

	mu      sync.Mutex
	clients map[*wsClient]struct{}

	// userState — ярлык настроения человека по последним репликам (этап 9в,
	// локальный детектор moodread): «устал», «расстроился», «рад», «злится»,
	// «тревожится» или "". Обновляется на КАЖДУЮ реплику (и без ключа тоже),
	// живёт до следующей реплики. В память и наружу не уходит — это состояние
	// текущей беседы.
	userState string
}

// UserState — ярлык настроения человека по последним репликам ("" — нейтрально).
func (h *Hub) UserState() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.userState
}

func (h *Hub) setUserState(s string) {
	h.mu.Lock()
	h.userState = s
	h.mu.Unlock()
}

// MemoryStore — долгая память для хаба (реализация — internal/memory.Store).
type MemoryStore interface {
	Remember(text, source string, at time.Time) (int64, error)
	Archive(role, text string, at time.Time) error
}

// SetMemory подключает долгую память (команда «запомни:»).
func (h *Hub) SetMemory(m MemoryStore) { h.memory = m }

// SetMoodEvents подключает канал внутреннего состояния (этап 5): реплика
// человека превращается в событие mood.EvUser.
func (h *Hub) SetMoodEvents(ch chan<- mood.Event) { h.moodEvents = ch }

// SetVoice сообщает хабу про озвучку (этап 7): она попадает в welcome, и
// фронтенд знает, просить ли /tts для реплик.
func (h *Hub) SetVoice(v *VoiceInfo) { h.voice = v }

// SetSTT сообщает хабу про голосовой ввод (этап 7): флаг попадает в welcome,
// фронтенд по нему показывает кнопку микрофона.
func (h *Hub) SetSTT(v *STTInfo) { h.stt = v }

// SetWake сообщает хабу про wake word (этап 7): флаг попадает в welcome.
func (h *Hub) SetWake(v *WakeInfo) { h.wake = v }

// wakeRe — узнаёт её имя в расшифрованном сегменте. Две формы:
//   - имя в начале фразы — по звучанию целиком («мира …»), и в виде, где
//     whisper съедал последний гласный («мир …» — так он пишет и «мир», и
//     «мира» одинаково, поэтому форма принята с оговоркой ниже);
//   - имя сразу после запятой («привет, Мира!») — только полная форма «мира».
//
// За именем не должно быть буквы: «мираж», «мирославная» — не имя.
var wakeRe = regexp.MustCompile(`(?:^|,\s*)мир(?:а)?(?:[^а-яёa-яё]|$)`)

// wakeGenitive — родительный от «мир» («край мира», «картина мира»), где
// полная форма «мира» не является обращением.
var wakeGenitive = regexp.MustCompile(`[\sа-яёa-яё]{2,}мира(?:[^а-яёa-яё]|$)`)

// wakeShortWorld — короткая «мир» в начале без гласной: предлог+«мир»
// («в мир», «о мир», «под мир») — не обращение. Обращения через предлог
// не бывает: «о, Мира!» — это полное «мира».
var wakeShortWorld = regexp.MustCompile(`(?:^|[^а-яёa-яё])(?:в|о|под|на|об|ко|за|меж)\s+мир(?:[^а-яёa-яё]|$)`)

// wakeLatinRunes — латиница в распознанном тексте = деградированная запись:
// на чистом русском whisper пишет кириллицей (проверено на живой цепочке),
// а на шуме пересэмплирования страницы сыпется в латинские литеры-двойники.
// «Мир, приверьте.» с латинской «i» на экране не отличить от кириллической.
var wakeLatinRunes = regexp.MustCompile(`[abceHKoepcxyAKMEHTiіІ]`)

// wakeTranslate — перевод латинских двойников в кириллицу. Латиница в
// распознанном русском = битая кириллица деградированной записи. Переводим
// по карте (wakeLatin); переводится весь текст — частичная замена убивает
// имя («Miра» с латинской i остаётся не-именем).
func wakeTranslate(s string) string {
	return wakeLatin.Replace(s)
}

// wakeLatin — карта визуальных двойников латиницы→кириллицы: латинские
// литеры в распознанном русском тексте = битая кириллица деградированной
// записи, по карте переводим весь текст (частичная замена убивает имя).
// «u»→«и»: в латинице u читается как «ю», но в битой кириллице u — это
// «и» («Mup» = «Мир», не «Мур»); на русском Whisper «u» как ю пишет через
// «y», которая и остаётся «у».
var wakeLatin = strings.NewReplacer(
	"A", "А", "a", "а", "B", "В", "b", "в", "C", "С", "c", "с", "E", "Е", "e", "е",
	"H", "Н", "h", "н", "I", "И", "i", "и", "J", "Й", "j", "й", "K", "К", "k", "к",
	"L", "Л", "l", "л", "M", "М", "m", "м", "N", "Н", "n", "н", "O", "О", "o", "о",
	"P", "Р", "p", "р", "Q", "Я", "q", "я", "R", "Г", "r", "г", "S", "С", "s", "с",
	"T", "Т", "t", "т", "U", "И", "u", "и", "X", "Х", "x", "х", "Y", "Ы", "y", "ы",
	"Z", "З", "z", "з",
	// «і» (украинская i, U+0456/U+0406) — whisper выдаёт её вместо «и» на
	// деградированной записи; на экране не отличить, а «мiр» ≠ «мир».
	"і", "и", "І", "И",
)

// WakeMention — прозвучало ли в тексте её имя. Регистр и «ё» не важны.
// Экспортировано: роут /wake проверяет текст именно этим правилом, и тесты
// гоняют тот же код, что работает в бою.
//
// Ложные срабатывания отсекаются после совпадения: полная «мира» внутри
// фразы («картина мира») — родительный от «мир», короткая «мир» в начале
// после предлога («в мир») — не обращение.
func WakeMention(text string) bool {
	t := strings.ToLower(strings.TrimSpace(text))
	t = strings.ReplaceAll(t, "ё", "е")
	if wakeLatinRunes.MatchString(t) {
		// Текст с примесью латиницы — битая кириллица, переводим весь.
		t = wakeTranslate(t)
	}
	if !wakeRe.MatchString(t) {
		return false
	}
	// Полная «мира» после слов — это «мира» от слова «мир» («картина мира»).
	// Обращение «привет, Мира!» с этим совпадением не путаем: оно через запятую.
	if wakeGenitive.MatchString(t) && !regexp.MustCompile(`,[\s]*мира(?:[^а-яёa-яё]|$)`).MatchString(t) {
		return false
	}
	if wakeShortWorld.MatchString(t) {
		return false
	}
	return true
}

// HandleWake — в речевом сегменте с /wake прозвучало её имя (wake word,
// этап 7). Что происходит:
//   - реплика человека — событие внутреннего состояния (как напечатанная);
//   - окно получает WakeMsg: фронтенд будит её (если спит), она оживает и
//     коротко отвечает на имя;
//   - сказанные ей слова идут в разговорную очередь как напечатанные —
//     на «Мира, которое время?» она отвечает по сути, а не только кивком.
//
// Ключа нет — разговорная часть молчит, разбуживание всё равно работает.
func (h *Hub) HandleWake(heard string) {
	h.notifyUserTalk()
	h.Broadcast(WakeMsg{Type: TypeWake, Heard: heard})

	if h.responder == nil || !h.responder.Ready() {
		return
	}
	h.mu.Lock()
	var c *wsClient
	for cl := range h.clients {
		c = cl
		break
	}
	h.mu.Unlock()
	if c == nil {
		return
	}
	select {
	case c.chat <- heard:
	default:
		// Очередь полна — она ещё отвечает на прошлое; разбудили и так,
		// слова не теряются: человек их слышал в ответе на имя.
	}
}

// NewHub собирает хаб. responder может быть nil — тогда разговор выключен
// (окно скажет об этом прямо, а не будет молчать).
func NewHub(log *slog.Logger, store *state.Store, avatar config.Avatar, responder Responder) *Hub {
	return &Hub{
		log:       log,
		store:     store,
		avatar:    avatar,
		responder: responder,
		clients:   make(map[*wsClient]struct{}),
	}
}

// SetKeyHint задаёт подсказку про ключ API (см. keyHint).
func (h *Hub) SetKeyHint(s string) { h.keyHint = strings.TrimSpace(s) }

// SetFocusTracker подключает трекер активного окна (см. пакет focus).
// Без него сообщения focus просто игнорируются — окну незачем знать, что
// сервер не собирает этот контекст.
func (h *Hub) SetFocusTracker(t *focus.Tracker) { h.focus = t }

// GetFocus — активное окно человека (для /healthz и проверок).
func (h *Hub) GetFocus() (focus.Snapshot, bool) {
	if h.focus == nil {
		return focus.Snapshot{}, false
	}
	return h.focus.Snapshot()
}

// wsClient — одно соединение с окном.
type wsClient struct {
	hub  *Hub
	conn *websocket.Conn
	send chan []byte
	// client — что за оболочка (web/electron): нужно в логах.
	client string
	// chat — очередь реплик человека; разбирает её отдельная горутина.
	chat chan string
	// done закрывается при отключении: разговор должен остановиться, а не
	// продолжать отвечать в закрытую очередь.
	done chan struct{}
	once sync.Once
	// mu + closed защищают очередь отправки: после закрытия канал трогать
	// нельзя (отправка в закрытый канал — паника всего сервера).
	mu     sync.Mutex
	closed bool
}

// upgrader — правила апгрейда HTTP → WebSocket.
//
// CheckOrigin оставлен на localhost: сервер локальный, и принимать WS с чужих
// сайтов (страница в браузере может подключиться к 127.0.0.1) нельзя — иначе
// любая вкладка управляла бы персонажем.
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true // оболочка Electron/Tauri ходит без Origin
		}
		return isLocalOrigin(origin)
	},
}

func isLocalOrigin(origin string) bool {
	for _, p := range []string{
		"http://127.0.0.1", "http://localhost", "https://127.0.0.1", "https://localhost",
		"tauri://localhost", "app://localhost", // Tauri
	} {
		if len(origin) >= len(p) && origin[:len(p)] == p {
			return true
		}
	}
	return false
}

// HandleWS — точка входа /ws.
func (h *Hub) HandleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.log.Warn("апгрейд WebSocket не удался", "err", err, "remote", r.RemoteAddr)
		return
	}
	c := &wsClient{
		hub:    h,
		conn:   conn,
		send:   make(chan []byte, sendQueue),
		chat:   make(chan string, chatQueue),
		done:   make(chan struct{}),
		closed: false,
	}
	h.add(c)
	h.log.Info("окно подключилось", "remote", r.RemoteAddr)

	go c.writeLoop()
	go c.chatLoop()
	c.readLoop()
}

// Position — сохранённая позиция окна (для /healthz: по ней видно, не уехало ли
// окно за пределы экрана).
func (h *Hub) Position() state.Position { return h.store.Snapshot().Position }

// Count — сколько окон сейчас подключено (для /healthz и тестов).
func (h *Hub) Count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

// Broadcast отправляет кадр всем, кто успевает читать.
func (h *Hub) Broadcast(v any) int {
	raw := encode(v)
	if raw == nil {
		return 0
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for c := range h.clients {
		if c.enqueue(raw) {
			n++
		}
	}
	return n
}

func (h *Hub) add(c *wsClient) {
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
}

func (h *Hub) remove(c *wsClient) {
	h.mu.Lock()
	_, ok := h.clients[c]
	delete(h.clients, c)
	h.mu.Unlock()
	if !ok {
		return
	}
	// Сначала останавливаем разговор, потом закрываем очередь отправки: так
	// готурина ответа не может дописать кадр в уже закрытый канал.
	c.once.Do(func() { close(c.done) })
	c.close()
	h.log.Info("окно отключилось", "client", c.client)
}

// close закрывает очередь отправки ровно один раз.
func (c *wsClient) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	close(c.send)
}

// enqueue кладёт кадр в очередь клиента. false — очередь полна или соединение
// уже закрыто: клиент не читает, кадр теряется (ждём таймаута и закрытия, а не
// растим память).
func (c *wsClient) enqueue(raw []byte) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return false
	}
	select {
	case c.send <- raw:
		return true
	default:
		return false
	}
}

// sendTo отправляет кадр ОДНОМУ клиенту.
func (c *wsClient) sendTo(v any) {
	if raw := encode(v); raw != nil {
		c.enqueue(raw)
	}
}

func (c *wsClient) readLoop() {
	defer func() {
		c.hub.remove(c)
		_ = c.conn.Close()
	}()
	c.conn.SetReadLimit(maxMessageSize)
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	for {
		_, raw, err := c.conn.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				var ce *websocket.CloseError
				if !errors.As(err, &ce) {
					c.hub.log.Debug("чтение закрыто", "err", err)
				}
			}
			return
		}
		var in Incoming
		if err := json.Unmarshal(raw, &in); err != nil {
			c.sendTo(ErrorMsg{Type: TypeError, Error: "кадр не разобран как JSON"})
			continue
		}
		c.hub.handle(c, in)
	}
}

func (c *wsClient) writeLoop() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		_ = c.conn.Close()
	}()
	for {
		select {
		case raw, ok := <-c.send:
			if !ok {
				_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, raw); err != nil {
				return
			}
		case <-ticker.C:
			if err := c.conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
				return
			}
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// handleSay принимает реплику человека.
//
// Чего здесь принципиально нет: вызова модели. Ответ занимает секунды, а окно
// продолжает жить (его таскают мышью — эти кадры идут через тот же readLoop),
// поэтому реплика ставится в очередь, а отвечает отдельная горутина — см.
// chatLoop.
func (h *Hub) handleSay(c *wsClient, raw string) {
	text := strings.TrimSpace(raw)
	if text == "" {
		c.sendTo(ReplyErrorMsg{Type: TypeReplyError, Error: "Пустая реплика — писать нечего."})
		return
	}
	if n := len([]rune(text)); n > maxSayRunes {
		c.sendTo(ReplyErrorMsg{Type: TypeReplyError,
			Error: "Слишком длинная реплика: " + strconv.Itoa(n) + " знаков, а помещается " + strconv.Itoa(maxSayRunes) + "."})
		return
	}
	// «запомни: …» — команда, а не реплика для ответа: она разборчива без
	// ключа модели (память — локальная), поэтому проверяется ДО готовности
	// разговорной части.
	if fact, ok := parseRemember(text); ok {
		h.handleRemember(c, text, fact)
		return
	}
	// Реплика человека — событие внутреннего состояния (этап 5) независимо от
	// того, будет ли ответ: «он написал» происходит даже без ключа.
	h.notifyUserTalk()
	// Эмпатия (этап 9в): тон реплики читается локально и становится ярлыком
	// текущей беседы («он устал») — для промпта и кадра ответа окну.
	h.setUserState(moodread.Read(text))
	// Ключа нет — говорим это сразу и текстом, который объясняет, что делать.
	// Ставить такую реплику в очередь незачем: ответа не будет.
	if h.responder == nil || !h.responder.Ready() {
		c.sendTo(ReplyErrorMsg{Type: TypeReplyError, Error: h.notReadyText()})
		return
	}
	select {
	case c.chat <- text:
	default:
		c.sendTo(ReplyErrorMsg{Type: TypeReplyError, Error: "Я ещё не договорила — подожди пару секунд."})
	}
}

// notifyUserTalk — сообщение человека как событие состояния (этап 5).
// Буфер переполнен — событие теряется: цикл состояния догонит по времени,
// а хаб не имеет права ждать.
func (h *Hub) notifyUserTalk() {
	if h.moodEvents == nil {
		return
	}
	select {
	case h.moodEvents <- mood.EvUser:
	default:
	}
}

// rememberPrefix — с чего начинается команда запоминания.
const rememberPrefix = "запомни:"

// parseRemember — эта реплика команда «запомни: …»? Регистр не важен.
// Вторая строка — текст факта (может быть пустой: «запомни:» без текста).
func parseRemember(text string) (string, bool) {
	lower := strings.ToLower(strings.TrimSpace(text))
	if !strings.HasPrefix(lower, rememberPrefix) {
		return "", false
	}
	fact := strings.TrimSpace(text[len(rememberPrefix):])
	return fact, true
}

// handleRemember — команда «запомни:»: факт в память, подтверждение в окно.
// Без ключа модели работает: память локальная, модель здесь не нужна.
func (h *Hub) handleRemember(c *wsClient, whole, fact string) {
	if h.memory == nil {
		c.sendTo(ReplyErrorMsg{Type: TypeReplyError,
			Error: "Память не подключена — «запомни:» сейчас не работает."})
		return
	}
	if fact == "" {
		c.sendTo(ReplyErrorMsg{Type: TypeReplyError,
			Error: "После «запомни:» нужен текст: что именно запомнить."})
		return
	}
	now := time.Now()
	if _, err := h.memory.Remember(fact, "user", now); err != nil {
		h.log.Error("факт не записан", "err", err)
		c.sendTo(ReplyErrorMsg{Type: TypeReplyError,
			Error: "В память не записалось: " + err.Error()})
		return
	}
	// И саму команду, и подтверждение кладём в архив разговора: саммари и
	// восстановление истории видят разговор целиком.
	_ = h.memory.Archive(chat.RoleUser, whole, now)
	confirm := "Запомнила: " + fact
	_ = h.memory.Archive(chat.RoleAssistant, confirm, now)
	c.sendTo(ReplyMsg{Type: TypeReply, Text: confirm, At: now.UTC()})
	h.log.Info("запомнен факт", "знаков", len([]rune(fact)))
}

// notReadyText — почему ответа не будет и что с этим делать.
func (h *Hub) notReadyText() string {
	status := "Разговор не настроен."
	if h.responder != nil {
		status = h.responder.Status()
	}
	if h.keyHint != "" {
		status += " " + h.keyHint
	}
	return status
}

// maxAppRunes — предел длины имени процесса. Имена процессов длинные не бывают,
// поэтому всё длиннее — обрывок данных, а не приложение.
const maxAppRunes = 120

// selftestWindowPrefix — маркер тестового окна помощника (см. cmd/focuswatch,
// -selftest): такие строки страница использует для проверки посадки, сервер
// их не принимает.
const selftestWindowPrefix = "SELFTEST-"

// handleFocus принимает активное окно человека (focuswatch → страница → сервер).
//
// Здесь принципиально нет никакого «обсуждения» кадра: трекер молча обновляется,
// и каждая следующая реплика Miры соберётся уже с новой строкой контекста.
// Окно само отвечает за то, что присылает только смены (см. cmd/focuswatch).
func (h *Hub) handleFocus(in Incoming) {
	if h.focus == nil {
		return // трекер не подключён — собираем молча, без кадров в ответ
	}
	app := strings.TrimSpace(in.App)
	if app == "" {
		return
	}
	// Строки тестового окна (самопроверка оболочки гоняет посадку «на окне»
	// через помощник с -selftest) не имеют права менять контекст рабочей Миры:
	// заголовок-маркер — и строка выбрасывается, как будто её не было.
	if strings.HasPrefix(strings.TrimSpace(in.Title), selftestWindowPrefix) {
		return
	}
	if len([]rune(app)) > maxAppRunes {
		app = string([]rune(app)[:maxAppRunes])
	}
	title := focus.CleanTitle(in.Title)
	idle := time.Duration(0)
	if in.Idle != nil && *in.Idle > 0 {
		// Потолок — десять суток: больше не бывает, а значение «2 миллиарда
		// секунд» от кривого клиента улетело бы в вечное «он исчез».
		d := time.Duration(*in.Idle) * time.Second
		if d < 10*24*time.Hour {
			idle = d
		}
	}
	kind, label := focus.Classify(app)
	h.focus.UpdateWithIdle(app, title, time.Now(), idle)
	h.log.Debug("активное окно", "process", app, "kind", kind, "label", label, "idle", idle)
}

// chatLoop разбирает очередь реплик окна.
//
// Отдельная горутина на соединение: она может ждать модель секунды, а читать
// канал в это время обязан кто-то другой.
func (c *wsClient) chatLoop() {
	for {
		select {
		case <-c.done:
			return
		case text := <-c.chat:
			c.reply(text)
		}
	}
}

// reply спрашивает компаньона и отдаёт его ответ окну.
func (c *wsClient) reply(text string) {
	ctx, cancel := context.WithTimeout(context.Background(), c.hub.replyTimeout())
	defer cancel()

	c.sendTo(ThinkingMsg{Type: TypeThinking, On: true})
	// Индикатор гасим в ЛЮБОМ случае. Фронтенд подстраховывается таймаутом, но
	// если «думает» остаётся горящим — это уже наш баг, а не его.
	defer c.sendTo(ThinkingMsg{Type: TypeThinking, On: false})

	start := time.Now()
	answer, err := c.hub.responder.Answer(ctx, text, c.hub.UserState())
	if err != nil {
		if errors.Is(err, llm.ErrNoKey) {
			// Не сбой, а состояние: человеку нужен текст про ключ, а не тревога.
			c.hub.log.Warn("ответа нет: ключ не настроен", "err", err)
			c.sendTo(ReplyErrorMsg{Type: TypeReplyError, Error: c.hub.explain(err)})
			return
		}
		c.hub.log.Error("ответ не получился", "err", err, "ms", time.Since(start).Milliseconds())
		c.sendTo(ReplyErrorMsg{Type: TypeReplyError, Error: c.hub.explain(err)})
		return
	}
	c.hub.log.Info("ответ", "ms", time.Since(start).Milliseconds(), "знаков", len([]rune(answer)))
	c.sendTo(ReplyMsg{Type: TypeReply, Text: answer, At: time.Now().UTC(),
		UserState: c.hub.UserState()})
}

// replyTimeout — дедлайн на всю операцию: таймаут модели плюс запас.
func (h *Hub) replyTimeout() time.Duration {
	t := time.Duration(0)
	if h.responder != nil {
		t = h.responder.Timeout()
	}
	if t <= 0 {
		t = 30 * time.Second
	}
	return t + replyMargin
}

// explain превращает ошибку в текст для человека.
//
// Ошибки модели уже написаны по-русски и по делу (см. llm.StatusError), поэтому
// здесь только выбор: показать её как есть или объяснить частные случаи, где
// «что делать» важнее, чем «что случилось».
func (h *Hub) explain(err error) string {
	if errors.Is(err, llm.ErrNoKey) {
		if t := h.notReadyText(); t != "" {
			return t
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "Модель не ответила вовремя. Похоже, канал медленный или API занят — попробуй ещё раз."
	}
	var se *llm.StatusError
	if errors.As(err, &se) {
		return se.Error()
	}
	return "Не получилось ответить: " + err.Error()
}

// handle разбирает сообщение клиента.
func (h *Hub) handle(c *wsClient, in Incoming) {
	switch in.Type {
	case TypeHello:
		c.client = in.Client
		if in.Version != 0 && in.Version != protocolVersion {
			h.log.Warn("версия протокола клиента отличается",
				"client", in.Client, "client_version", in.Version, "server_version", protocolVersion)
		}
		welcome := Welcome{
			Type:       TypeWelcome,
			Protocol:   protocolVersion,
			ServerTime: time.Now().UTC(),
			State:      h.store.Snapshot(),
			Avatar: AvatarInfo{
				Name:   h.avatar.Name,
				Width:  h.avatar.Width,
				Height: h.avatar.Height,
				Sprite: h.avatar.Sprite,
			},
			Voice: h.voice,
			STT:   h.stt,
			Wake:  h.wake,
		}
		if h.responder != nil {
			welcome.History = h.responder.History()
		}
		c.sendTo(welcome)
	case TypePosition:
		if in.X == nil || in.Y == nil {
			c.sendTo(ErrorMsg{Type: TypeError, Error: "position без x/y"})
			return
		}
		st, err := h.store.SetPosition(state.Position{X: *in.X, Y: *in.Y})
		if err != nil {
			h.log.Error("позиция не сохранилась", "err", err)
			c.sendTo(ErrorMsg{Type: TypeError, Error: "позицию не удалось сохранить"})
			return
		}
		h.log.Debug("позиция сохранена", "x", st.Position.X, "y", st.Position.Y)
		c.sendTo(StateMsg{Type: TypeState, Position: st.Position, Saved: true})
	case TypeSay:
		h.handleSay(c, in.Text)
	case TypeFocus:
		h.handleFocus(in)
	case TypePing:
		c.sendTo(PongMsg{Type: TypePong, ServerTime: time.Now().UTC()})
	default:
		c.sendTo(ErrorMsg{Type: TypeError, Error: "неизвестный тип сообщения: " + in.Type})
	}
}
