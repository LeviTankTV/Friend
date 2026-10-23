// Package focus — «что у человека на экране».
//
// Компаньону нужен ТОЛЬКО заголовок и имя процесса активного окна: этого хватает,
// чтобы знать «редактор кода», «браузер», «игра» — и не говорить универсальные
// вещи там, где уместна личная реплика. Ничего, кроме названия процесса и
// заголовка, не читается и никуда, кроме текста запроса к модели, не отправляется.
//
// Классификация — список известных приложений с русскими подписями. Неизвестное
// приложение получает label из имени процесса и явное предупреждение модели
// «не выдумывай»: молчаливый догадливый ассистент опаснее честного незнания.
package focus

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"friend/internal/i18n"
)

// Kind — к какому делу относится приложение. Нужен не для красоты, а для
// подсказки тона: «в редакторе кода» и «в игре» требуют разного обращения.
type Kind string

const (
	KindNone      Kind = "none"      // рабочий стол, проводник — говорить не о чем
	KindCode      Kind = "code"      // редакторы кода и IDE
	KindBrowser   Kind = "browser"   // браузеры
	KindMessenger Kind = "messenger" // мессенджеры
	KindGame      Kind = "game"      // игры и игровые лаунчеры
	KindMusic     Kind = "music"     // плееры
	KindOffice    Kind = "office"    // офис, документы
	KindTerminal  Kind = "terminal"  // терминалы и оболочки командной строки
	KindOther     Kind = "other"     // всё, что не узнали
	KindCompanion Kind = "companion" // окно самой Миры — в промпт не попадает
)

type appInfo struct {
	kind  Kind
	label string
}

// known — известные приложения: имя процесса (без .exe, в нижнем регистре) →
// тип и человеческая подпись. Список намеренно короткий: угадывать тип по
// незнакомому имени — путь к выдумкам; неизвестное приложение честно проходит
// как KindOther с предупреждением.
var known = map[string]appInfo{
	// Редакторы и IDE.
	"code":          {KindCode, "VS Code (редактор кода)"},
	"code-insiders": {KindCode, "VS Code Insiders (редактор кода)"},
	"cursor":        {KindCode, "Cursor (редактор кода)"},
	"windsurf":      {KindCode, "Windsurf (редактор кода)"},
	"zed":           {KindCode, "Zed (редактор кода)"},
	"idea":          {KindCode, "IntelliJ IDEA (IDE)"},
	"idea64":        {KindCode, "IntelliJ IDEA (IDE)"},
	"goland":        {KindCode, "GoLand (IDE для Go)"},
	"goland64":      {KindCode, "GoLand (IDE для Go)"},
	"pycharm":       {KindCode, "PyCharm (IDE)"},
	"pycharm64":     {KindCode, "PyCharm (IDE)"},
	"webstorm":      {KindCode, "WebStorm (IDE)"},
	"webstorm64":    {KindCode, "WebStorm (IDE)"},
	"clion":         {KindCode, "CLion (IDE)"},
	"clion64":       {KindCode, "CLion (IDE)"},
	"rustrover":     {KindCode, "RustRover (IDE)"},
	"devenv":        {KindCode, "Visual Studio (редактор кода)"},
	"sublime_text":  {KindCode, "Sublime Text (редактор кода)"},
	"subl":          {KindCode, "Sublime Text (редактор кода)"},
	"notepad++":     {KindCode, "Notepad++ (редактор текста)"},
	"nvim":          {KindCode, "Neovim (редактор кода)"},
	"vim":           {KindCode, "Vim (редактор кода)"},
	"neovim":        {KindCode, "Neovim (редактор кода)"},

	// Браузеры.
	"chrome":   {KindBrowser, "Chrome (браузер)"},
	"msedge":   {KindBrowser, "Edge (браузер)"},
	"firefox":  {KindBrowser, "Firefox (браузер)"},
	"opera":    {KindBrowser, "Opera (браузер)"},
	"brave":    {KindBrowser, "Brave (браузер)"},
	"vivaldi":  {KindBrowser, "Vivaldi (браузер)"},
	"iexplore": {KindBrowser, "Internet Explorer (браузер)"},

	// Мессенджеры.
	"telegram":  {KindMessenger, "Telegram"},
	"telegramx": {KindMessenger, "Telegram"},
	"discord":   {KindMessenger, "Discord"},
	"whatsapp":  {KindMessenger, "WhatsApp"},
	"skype":     {KindMessenger, "Skype"},
	"dingtalk":  {KindMessenger, "DingTalk"},

	// Игры и игровые лаунчеры (минимально: своя игра — процесс неизвестный,
	// модель честно предупредят «не выдумывай»).
	"steam":                   {KindGame, "Steam"},
	"steamwebhelper":          {KindGame, "Steam"},
	"steamoverlay":            {KindGame, "Steam"},
	"epicgameslauncher":       {KindGame, "Epic Games Launcher"},
	"riotclientservices":      {KindGame, "Riot Client"},
	"valorant-win64-shipping": {KindGame, "Valorant"},
	"minecraft":               {KindGame, "Minecraft"},
	"csgo":                    {KindGame, "Counter-Strike"},
	"cs2":                     {KindGame, "Counter-Strike 2"},
	"gta5":                    {KindGame, "GTA V"},
	"eldenring":               {KindGame, "Elden Ring"},
	"witcher3":                {KindGame, "Ведьмак 3"},

	// Плееры.
	"spotify":    {KindMusic, "Spotify (музыка)"},
	"vlc":        {KindMusic, "VLC (плеер)"},
	"mpv":        {KindMusic, "mpv (плеер)"},
	"itunes":     {KindMusic, "iTunes (плеер)"},
	"foobar2000": {KindMusic, "foobar2000 (плеер)"},

	// Офис и документы.
	"winword":  {KindOffice, "Word (документ)"},
	"word":     {KindOffice, "Word (документ)"},
	"excel":    {KindOffice, "Excel (таблица)"},
	"powerpnt": {KindOffice, "PowerPoint (презентация)"},
	"outlook":  {KindOffice, "Outlook (почта)"},
	"onenote":  {KindOffice, "OneNote (заметки)"},
	"acrobat":  {KindOffice, "Acrobat (PDF)"},
	"acronim":  {KindOffice, "Acrobat (PDF)"},

	// Терминалы.
	"windowsterminal": {KindTerminal, "терминал Windows"},
	"wt":              {KindTerminal, "терминал Windows"},
	"terminal":        {KindTerminal, "терминал"},
	"powershell":      {KindTerminal, "PowerShell"},
	"pwsh":            {KindTerminal, "PowerShell"},
	"cmd":             {KindTerminal, "командная строка"},

	// Рабочий стол и оболочки: говорить не о чём.
	"explorer":                {KindNone, ""},
	"taskview":                {KindNone, ""},
	"shelview":                {KindNone, ""},
	"searchapp":               {KindNone, ""},
	"searchhost":              {KindNone, ""},
	"startmenuexperiencehost": {KindNone, ""},

	// Окно самой Миры (Electron): собственное окно — не «приложение человека».
	"electron": {KindCompanion, ""},
}

// normalizeProcess — имя процесса к виду ключа known: без пути, без .exe,
// в нижнем регистре («VALORANT-Win64-Shipping.exe» → «valorant-win64-shipping»).
func normalizeProcess(p string) string {
	p = strings.TrimSpace(p)
	// Последний разделитель пути (Windows — обратный слэш, прямим под него).
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		p = p[i+1:]
	}
	p = strings.ToLower(p)
	p = strings.TrimSuffix(p, ".exe")
	return strings.TrimSpace(p)
}

// Classify определяет тип и подпись приложения по имени процесса.
// Неизвестное имя → KindOther с подписью из самого имени: выдумывать label
// из «foo_bar.exe» незачем, а KindOther включает в промпте запрет на догадки.
func Classify(process string) (Kind, string) {
	key := normalizeProcess(process)
	if key == "" {
		return KindNone, ""
	}
	if info, ok := known[key]; ok {
		return info.kind, info.label
	}
	label := key
	// Подпись из имени процесса: подчёркивания и дефисы в именах exe —
	// «VALORANT-Win64-Shipping», «goland64» — для читаемости моделью.
	return KindOther, label
}

// MaxTitleRunes — заголовок окна коротким не бывает, а в промпт он идёт целиком:
// длинный заголовок жжёт токены и раздувает контекст.
const MaxTitleRunes = 160

// CleanTitle приводит заголовок окна к виду для промпта: без управляющих
// символов, с одним пробелом между словами, обрезанный по длине.
func CleanTitle(title string) string {
	var b strings.Builder
	space := false
	for _, r := range title {
		if unicode.IsControl(r) {
			space = b.Len() > 0
			continue
		}
		if unicode.IsSpace(r) {
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
		if len([]rune(b.String())) >= MaxTitleRunes {
			b.WriteString("…")
			break
		}
	}
	return strings.TrimSpace(b.String())
}

// Snapshot — что известно об активном окне на момент замера.
type Snapshot struct {
	Process string    `json:"process"`
	Title   string    `json:"title"`
	Kind    Kind      `json:"kind"`
	Label   string    `json:"label"`
	Since   time.Time `json:"since"`   // когда человек перешёл в это приложение
	IdleAt  time.Time `json:"idle_at"` // когда в последний раз двигали мышью/клавиатурой (нулевой — данных нет)
}

// Tracker помнит текущее активное окно. Это единственный источник правды для
// промпта: окно обновляется извне (оболочка → страница → сервер), а читает его
// горутина ответа, поэтому всё под мьютексом.
type Tracker struct {
	mu     sync.Mutex
	app    string
	title  string
	since  time.Time
	idleAt time.Time
	have   bool
}

// New создаёт трекер («приложение ещё ни разу не приходило»).
func New() *Tracker { return &Tracker{} }

// Update отмечает активное окно без данных о мыши.
func (t *Tracker) Update(app, title string, at time.Time) { t.UpdateWithIdle(app, title, at, 0) }

// UpdateWithIdle отмечает активное окно и бездействие человека.
//
// Правило про «уже»: счёт времени («в GoLand уже 2 часа») начинается с
// ПОСЛЕДНЕЙ смены приложения. Смена заголовка внутри того же приложения —
// человек переключает вкладки, а не приложения — счёт не обнуляет.
// idle > 0 означает «столько шёл последний ввод мыши/клавиатуры»: idleAt
// обновляется только свежими данными (0 — данных в этом кадре нет).
func (t *Tracker) UpdateWithIdle(app, title string, at time.Time, idle time.Duration) {
	app = strings.TrimSpace(app)
	if app == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.have || t.app != app {
		t.app = app
		t.since = at
	}
	t.title = title
	if idle > 0 {
		t.idleAt = at.Add(-idle)
	} else if t.idleAt.IsZero() {
		// Кадров без idle никогда не было: считаем, что ввод был сейчас,
		// иначе «он исчез» сработает на пустом месте.
		t.idleAt = at
	}
	t.have = true
}

// Snapshot отдаёт текущее состояние: second — приходило ли вообще что-нибудь.
func (t *Tracker) Snapshot() (Snapshot, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.have {
		return Snapshot{}, false
	}
	kind, label := Classify(t.app)
	return Snapshot{Process: t.app, Title: t.title, Kind: kind, Label: label, Since: t.since, IdleAt: t.idleAt}, true
}

// kindHint — как обращаться к человеку, когда он в приложении этого типа.
// Это не «что он делает» (этого мы не знаем), а как себя вести.
func kindHint(d *i18n.Dict, k Kind) string {
	switch k {
	case KindCode:
		return d.KindHintCode
	case KindBrowser:
		return d.KindHintBrowser
	case KindMessenger:
		return d.KindHintMessenger
	case KindGame:
		return d.KindHintGame
	case KindMusic:
		return d.KindHintMusic
	case KindOffice:
		return d.KindHintOffice
	case KindTerminal:
		return d.KindHintTerminal
	default:
		return d.KindHintOther
	}
}

// humanDuration — «уже 2 ч 15 мин» по-человечески. Меньше минуты не упоминаем:
// «уже 30 секунд» звучит слежкой, а не заботой.
func humanDuration(d *i18n.Dict, td time.Duration) string {
	if td < time.Minute {
		return ""
	}
	if td >= 24*time.Hour {
		return d.DurOverDay
	}
	min := int(td.Minutes())
	if min < 60 {
		return fmt.Sprintf(d.DurMinutes, min)
	}
	h, m := min/60, min%60
	if m == 0 {
		return fmt.Sprintf(d.DurHours, h)
	}
	return fmt.Sprintf(d.DurHM, h, m)
}

// Line — строка контекста для системного промпта: «он сейчас в GoLand
// (IDE для Go), уже 2 ч — не перебивай». Пустая строка означает «говорить об
// активном окне нечего»: рабочий стол, окно самой Миры, ничего не пришло.
// Язык строки — язык персоны (этап 10).
//
// Правило запретов здесь, а не в характере: оно про ЧИСТУЮ механику контекста
// (у нас только имя процесса и заголовок), и менять её из mira.json незачем.
func Line(s Snapshot, now time.Time, lang string) string {
	if s.Kind == KindNone || s.Kind == KindCompanion {
		return ""
	}
	d := i18n.For(lang)
	label := s.Label
	if label == "" {
		label = s.Process
	}
	var b strings.Builder
	fmt.Fprintf(&b, d.FocusHeIn, label)
	if s.Title != "" {
		fmt.Fprintf(&b, d.FocusTitle, s.Title)
	}
	if dur := humanDuration(d, now.Sub(s.Since)); dur != "" {
		fmt.Fprintf(&b, d.FocusFor, dur)
	}
	b.WriteString(". ")
	b.WriteString(kindHint(d, s.Kind))
	b.WriteString(d.FocusTail)
	return b.String()
}

// KnownNames — список известных приложений (для логов и самопроверки).
func KnownNames() []string {
	names := make([]string, 0, len(known))
	for name := range known {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
