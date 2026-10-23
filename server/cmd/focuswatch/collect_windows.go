//go:build windows

package main

import (
	"encoding/json"
	"os"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// Вызовы берём из user32/kernel32 напрямую (stdlib syscall.NewLazyDLL):
// помощнику не нужен ни один внешний модуль — он собирается вместе с сервером
// одной командой go build и весит ровно столько, сколько времени тратит на
// запросы к API.
var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	procGetForeground      = user32.NewProc("GetForegroundWindow")
	procGetWindowTextLen   = user32.NewProc("GetWindowTextLengthW")
	procGetWindowText      = user32.NewProc("GetWindowTextW")
	procGetWindowRect      = user32.NewProc("GetWindowRect")
	procGetWindowThreadPID = user32.NewProc("GetWindowThreadProcessId")
	procGetLastInputInfo   = user32.NewProc("GetLastInputInfo")
	procOpenProcess        = kernel32.NewProc("OpenProcess")
	procQueryImageName     = kernel32.NewProc("QueryFullProcessImageNameW")
	procGetTickCount64     = kernel32.NewProc("GetTickCount64")
	procCloseHandle        = kernel32.NewProc("CloseHandle")
)

// processQueryLimited — минимальный доступ к процессу, нужный только чтобы
// прочесть имя exe. Права на «убить процесс» помощник не просит принципиально.
const processQueryLimited = 0x00001000

// collect — цикл опроса. Сменилось окно — строка сразу; окно то же — строка
// не чаще heartbeat (обновляет idle). 150 одинаковых строк в минуту — шум
// и лишняя работа на канале.
func collect(d time.Duration) int {
	// Закрытие stdin (оболочка умерла) — выходим. Без этого помощник
	// переживал бы родителя и висел бы в системе невидимкой.
	go func() {
		buf := make([]byte, 1)
		for {
			n, err := os.Stdin.Read(buf)
			if n == 0 || err != nil {
				os.Exit(0)
			}
		}
	}()

	enc := json.NewEncoder(os.Stdout)
	was := sample{Process: "\x00"} // заведомо не совпадает ни с каким ответом current()
	nextBeat := time.Time{}        // ноль — первая строка печатается сразу
	ticker := time.NewTicker(d)
	defer ticker.Stop()
	for {
		s := current()
		if s.Process != "" {
			changed := s.Process != was.Process || s.Title != was.Title
			if selftestMode {
				// В режиме самопроверки строка печатается каждый тик: окно
				// теста всегда «одинаковое», а самопроверке нужны свежие
				// границы мишени (оболочка подставляет их в каждый тик).
				changed = true
			}
			if changed || time.Now().Sub(nextBeat) >= 0 {
				was = s
				nextBeat = time.Now().Add(HEARTBEAT)
				if err := enc.Encode(s); err != nil {
					return 1 // stdout закрыт — родитель умер, работать дальше некому
				}
			}
		}
		<-ticker.C
	}
}

// current — активное окно прямо сейчас: имя процесса, заголовок, idle и
// прямоугольник окна. Пустой Process — окна нет (переключение рабочего стола,
// экран блокировки).
func current() sample {
	hwnd, _, _ := procGetForeground.Call()
	if hwnd == 0 {
		return sample{}
	}
	var pid uint32
	procGetWindowThreadPID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	s := sample{
		Process: processName(pid),
		Title:   windowTitle(hwnd),
		Idle:    idleSeconds(),
		Bounds:  windowRect(hwnd),
	}
	if selftestMode {
		// Режим самопроверки: живое окно человека не сообщаем, отдаём тестовое
		// (« SELFTEST-… »). Оболочка подставит в него границы своего окна —
		// сервер такие строки отбрасывает, контекст рабочей Миры не меняется.
		s.Process = "SelftestTarget.exe"
		s.Title = constSelftestTitle
		s.Idle = 1
		s.Bounds = [4]int{}
	}
	return s
}

// windowRect — прямоугольник окна в экранных координатах (x, y, ширина,
// высота). Нулевой прямоугольник = «геометрии нет», страница в этом случае
// сажать никуда не будет.
func windowRect(hwnd uintptr) [4]int {
	var r rect
	ok, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
	if ok == 0 {
		return [4]int{}
	}
	w := int(r.right - r.left)
	h := int(r.bottom - r.top)
	if w <= 0 || h <= 0 {
		return [4]int{}
	}
	return [4]int{int(r.left), int(r.top), w, h}
}

type rect struct {
	left, top, right, bottom int32
}

// idleSeconds — сколько секунд прошло с последнего движения мышью/клавиатурой.
// GetLastInputInfo отдаёт отметку по GetTickCount64, поэтому разница считается
// по той же шкале. Ошибка API → 0 = «данных нет», сервер не придумывает idle сам.
func idleSeconds() int {
	var info struct {
		cbSize uint32
		dwTime uint32
	}
	info.cbSize = uint32(unsafe.Sizeof(info))
	ok, _, _ := procGetLastInputInfo.Call(uintptr(unsafe.Pointer(&info)))
	if ok == 0 {
		return 0
	}
	ticks, _, _ := procGetTickCount64.Call()
	tick := uint64(ticks)
	// dwTime — uint32 (переполнение ~49 дней), tick64 — 64 бит: отматываем
	// ровно на разницу, переполнение 32-битной отметки само себя лечит.
	last := uint64(info.dwTime)
	if tick >= last {
		return int((tick - last) / 1000)
	}
	return 0
}

// processName — имя exe по pid. Путь целиком не нужен и не нужен полностью:
// серверу достаточно имени процесса (приватность: путь к программе — уже лишнее).
func processName(pid uint32) string {
	if pid == 0 {
		return ""
	}
	handle, _, _ := procOpenProcess.Call(processQueryLimited, 0, uintptr(pid))
	if handle == 0 {
		return "" // чужой защищённый процесс: имя недоступно, окно не сообщаем
	}
	defer procCloseHandle.Call(handle)

	buf := make([]uint16, 512)
	size := uint32(len(buf))
	ok, _, _ := procQueryImageName.Call(handle, 0,
		uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if ok == 0 || size == 0 {
		return ""
	}
	full := syscall.UTF16ToString(buf[:size])
	if i := strings.LastIndexAny(full, `/\`); i >= 0 {
		return full[i+1:]
	}
	return full
}

// windowTitle — заголовок активного окна (пустой у некоторых окон — это норма).
func windowTitle(hwnd uintptr) string {
	n, _, _ := procGetWindowTextLen.Call(hwnd)
	if n == 0 {
		return ""
	}
	const max = 300 // сервер обрезает строку для промпта жёстче; здесь — с запасом
	size := int(n)
	if size > max {
		size = max
	}
	buf := make([]uint16, size+1)
	procGetWindowText.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(size+1))
	return syscall.UTF16ToString(buf)
}
