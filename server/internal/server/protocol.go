package server

import (
	"encoding/json"
	"time"

	"friend/internal/chat"
	"friend/internal/state"
)

// protocolVersion — версия формата сообщений. Клиент присылает свою в hello;
// несовпадение не рвёт связь, но пишется в лог: так рассинхрон фронта и сервера
// видно сразу, а не по «странному поведению».
const protocolVersion = 1

// Типы сообщений. Строки — часть контракта с фронтендом (web/js/net.js).
const (
	// Клиент → сервер.
	TypeHello    = "hello"
	TypePosition = "position"
	TypePing     = "ping"
	// TypeSay — реплика человека (то, что он напечатал в окне ввода).
	TypeSay = "say"
	// TypeFocus — какое окно сейчас в фокусе у человека (имя процесса и
	// заголовок). Приходит только при СМЕНЕ окна, поэтому поток маленький.
	TypeFocus = "focus"

	// Сервер → клиент.
	TypeWelcome = "welcome"
	TypeState   = "state"
	TypePong    = "pong"
	TypeError   = "error"
	// TypeThinking — она думает (окно показывает индикатор, а не «тишину»).
	TypeThinking = "thinking"
	// TypeReply — её реплика (ответ на сказанное или собственная инициатива).
	TypeReply = "reply"
	// TypeReplyError — ответ не получился. Отдельный тип, а не TypeError:
	// ошибка протокола — это баг в коде, а «нет ключа» — сообщение человеку.
	TypeReplyError = "reply_error"
	// TypeWake — в речевом сегменте, досланном на /wake, прозвучало её имя
	// (wake word, этап 7): фронтенд будит её и отвечает на имя.
	TypeWake = "wake"
	// TypeLang — смена языка персонажа (этап 13): человек выбирает ru/en в
	// меню, сервер пересобирает характер, промпты, голос и wake и рассылает
	// ответный lang всем подключённым окнам.
	TypeLang = "lang"
)

// Incoming — сообщение от клиента. Все поля необязательные: неизвестный тип не
// должен ломать разбор, поэтому структура «плоская», а не интерфейс с
// типизированными вариантами.
type Incoming struct {
	Type    string `json:"type"`
	Client  string `json:"client,omitempty"`
	Version int    `json:"version,omitempty"`
	X       *int   `json:"x,omitempty"`
	Y       *int   `json:"y,omitempty"`
	// Text — реплика человека (say).
	Text string `json:"text,omitempty"`
	// App/Title — активное окно (focus): имя процесса и заголовок окна.
	// Единственные сведения об экране человека, доходящие до сервера.
	App   string `json:"app,omitempty"`
	Title string `json:"title,omitempty"`
	// Idle — секунд с последнего движения мыши/клавиатуры (focuswatch).
	// Отсутствует = данных нет, сервер не придумывает бездействие сам.
	Idle *int `json:"idle,omitempty"`
	// Language — код языка для смены (lang, этап 13): "ru" | "en".
	Language string `json:"language,omitempty"`
}

// LangMsg — язык применён (этап 13): рассылается всем окнам после SetLanguage,
// чтобы каждое окно перекрасило строки и показало правдивый переключатель.
type LangMsg struct {
	Type     string `json:"type"`
	Language string `json:"language"`
}

// Welcome — первый ответ на hello: состояние и кто в окне.
type Welcome struct {
	Type       string      `json:"type"`
	Protocol   int         `json:"protocol"`
	ServerTime time.Time   `json:"server_time"`
	State      state.State `json:"state"`
	Avatar     AvatarInfo  `json:"avatar"`
	// History — что уже было сказано за время жизни сервера. Окно
	// перезапускается чаще (правка фронтенда, перезагрузка страницы), и без
	// истории разговор каждый раз выглядел бы начатым с нуля.
	History []chat.Message `json:"history,omitempty"`
	// Voice — доступность озвучки (этап 7): фронтенд по этому флагу решает,
	// просить ли /tts для реплик. Nil — функция не настроена.
	Voice *VoiceInfo `json:"voice,omitempty"`
	// STT — доступность голосового ввода (этап 7): по флагу фронтенд
	// показывает кнопку микрофона. Nil — функция не настроена.
	STT *STTInfo `json:"stt,omitempty"`
	// Wake — доступность wake word (этап 7): локальный слушатель её имени.
	// Nil или Available=false — фоновый микрофон не включается никогда.
	Wake *WakeInfo `json:"wake,omitempty"`
}

// VoiceInfo — озвучка (этап 7): есть ли edge-tts и каким голосом она говорит.
type VoiceInfo struct {
	Available bool   `json:"available"`
	Engine    string `json:"engine,omitempty"`
	Voice     string `json:"voice,omitempty"`
}

// STTInfo — голосовой ввод (этап 7): есть ли локальный распознаватель.
type STTInfo struct {
	Available bool   `json:"available"`
	Engine    string `json:"engine,omitempty"`
}

// WakeInfo — wake word (этап 7): включена ли функция и есть ли чем слушать
// (тот же локальный распознаватель, что и у /stt).
type WakeInfo struct {
	Available bool   `json:"available"`
	Engine    string `json:"engine,omitempty"`
}

// WakeMsg — в речевом сегменте прозвучало её имя. Heard — расшифрованный
// сегмент целиком: фронтенд по нему будит её, а сказанные ей слова уходят
// в разговор как напечатанные (см. Hub.HandleWake).
type WakeMsg struct {
	Type  string `json:"type"`
	Heard string `json:"heard"`
}

// ReplyMsg — её реплика.
type ReplyMsg struct {
	Type string    `json:"type"`
	Text string    `json:"text"`
	At   time.Time `json:"at"`
	// Proactive — заговорила сама, без реплики человека. Пока всегда false:
	// инициатива — этап 4. Поле есть сразу, потому что облачко от «ответила» и
	// от «заговорила первой» выглядит по-разному, и фронтенд должен уметь
	// различать их с самого начала.
	Proactive bool `json:"proactive,omitempty"`
	// Emotion — эмоциональное оформление реплики, если она есть. Пока
	// используется одно значение: "sleep" — она засыпает, произнося фразу про
	// сон (идл-событие). Пусто — обычная реплика: лицо фронтенд выбирает сам.
	Emotion string `json:"emotion,omitempty"`
	// UserState — ярлык настроения человека по последним репликам (этап 9в,
	// локальный детектор moodread): фронтенд по нему меняет лицо аватара
	// (сочувствие, радость). Пусто — нейтрально, лицо не трогаем.
	UserState string `json:"user_state,omitempty"`
}

// ThinkingMsg — индикатор «думает»: включить/выключить.
type ThinkingMsg struct {
	Type string `json:"type"`
	On   bool   `json:"on"`
}

// ReplyErrorMsg — ответ не получился. Текст адресован человеку: он должен
// понимать, что делать, а не читать «error 500».
type ReplyErrorMsg struct {
	Type  string `json:"type"`
	Error string `json:"error"`
}

// AvatarInfo — что клиенту нужно знать о персонаже (имя показывается в подписи,
// спрайт приедет на этапе 6).
type AvatarInfo struct {
	Name string `json:"name"`
	// Language — язык персонажа из конфига (ru/en, этап 10): страница по нему
	// выбирает словарь интерфейса и её локальных фраз.
	Language string `json:"language,omitempty"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	Sprite   string `json:"sprite"`
}

// MoodInfo — внутреннее состояние (этап 5) для клиента: три числа и
// полоска «лица покоя» по настроению.
type MoodInfo struct {
	Mood      float64 `json:"mood"`
	Energy    float64 `json:"energy"`
	Affection float64 `json:"affection"`
	Band      string  `json:"band"`
}

// StateMsg — подтверждение сохранённого состояния: клиент получает ТУ ЖЕ
// позицию, что записана на диске, а не «свою предполагаемую». Кадр с одной
// MoodInfo (позиции нет) — обновление внутреннего состояния: лицо персонажа
// меняется, когда настроение заметно сдвинулось.
type StateMsg struct {
	Type     string         `json:"type"`
	Position state.Position `json:"position,omitempty"`
	Saved    bool           `json:"saved,omitempty"`
	Mood     *MoodInfo      `json:"mood,omitempty"`
}

// PongMsg — ответ на ping (проверка живости канала из фронтенда).
type PongMsg struct {
	Type       string    `json:"type"`
	ServerTime time.Time `json:"server_time"`
}

// ErrorMsg — ошибка протокола (неизвестный тип, битые координаты).
type ErrorMsg struct {
	Type  string `json:"type"`
	Error string `json:"error"`
}

// encode собирает кадр для отправки. Ошибка кодирования возможна только на
// структурах без каналов и функций — то есть не бывает; возвращаем nil, чтобы
// вызывающая сторона не ветвилась.
func encode(v any) []byte {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return raw
}
