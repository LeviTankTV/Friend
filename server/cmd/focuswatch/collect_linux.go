//go:build linux

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"friend/internal/x11"
)

// Linux-версия помощника: X11 по чистому Go-протоколу (см. internal/x11).
// Схема вывода — та же, что на Windows: {"process","title","idle","bounds"}.
//
// Wayland: глобального слежения за окнами из песочницы нет — помощник
// печатает одну строку с ясной причиной и выходит (окно работает без
// «понимания экрана», сервер просто не получает кадров focus).

const HEARTBEAT = 30 * time.Second

type sample struct {
	Process string   `json:"process"`
	Title   string   `json:"title"`
	Idle    int      `json:"idle,omitempty"`
	Bounds  [4]int   `json:"bounds,omitempty"`
}

func collect(d time.Duration) int {
	// Закрытие stdin (оболочка умерла) — выходим, как на Windows.
	go func() {
		buf := make([]byte, 1)
		for {
			n, err := os.Stdin.Read(buf)
			if n == 0 || err != nil {
				os.Exit(0)
			}
		}
	}()

	if os.Getenv("XDG_SESSION_TYPE") == "wayland" && os.Getenv("DISPLAY") == "" {
		fmt.Println(`{"error":"focuswatch: Wayland глобального слежения за окнами не даёт; в X11-сессии (Xorg) понимание окна работает"}`)
		return 0
	}

	enc := json.NewEncoder(os.Stdout)
	was := sample{Process: "\x00"}
	nextBeat := time.Time{}
	ticker := time.NewTicker(d)
	defer ticker.Stop()

	cl, err := x11.Connect()
	if err != nil {
		// Одна строка с причиной — и выход: сервер живёт без понимания окна.
		fmt.Println(fmt.Sprintf(`{"error":"focuswatch: %s"}`, jsonEscape(err.Error())))
		return 0
	}
	defer cl.Close()

	for {
		s := current(cl)
		if s.Process != "" {
			changed := s.Process != was.Process || s.Title != was.Title
			if selftestMode {
				changed = true
			}
			if changed || time.Now().Sub(nextBeat) >= 0 {
				was = s
				nextBeat = time.Now().Add(HEARTBEAT)
				if err := enc.Encode(s); err != nil {
					return 1
				}
			}
		}
		<-ticker.C
	}
}

// current — активное окно прямо сейчас. Пустой Process — окна нет
// (рабочий стол без фокуса, экран блокировки).
func current(cl *x11.Client) sample {
	w, err := cl.ActiveWindow()
	if err != nil || w == 0 {
		return sample{}
	}
	pid := cl.WindowPID(w)
	s := sample{
		Process: processName(pid),
		Title:   cl.WindowName(w),
		Idle:    cl.IdleSeconds(),
	}
	x, y, ww, hh := cl.WindowGeometry(w)
	if ww > 0 && hh > 0 {
		s.Bounds = [4]int{x, y, ww, hh}
	}
	if selftestMode {
		// Режим самопроверки — тестовое окно с маркером: оболочка подставит
		// в него границы своего окна; сервер такие строки отбрасывает.
		s.Process = "SelftestTarget"
		s.Title = constSelftestTitle
		s.Idle = 1
		s.Bounds = [4]int{}
	}
	return s
}

// processName — имя процесса по pid из /proc/<pid>/comm. Имя, не путь:
// приватность та же, что на Windows (QueryFullProcessImageName тоже берёт
// только имя exe). comm обрывает имя до 15 символов — это имя и отдаём.
func processName(pid uint32) string {
	if pid == 0 {
		return ""
	}
	raw, err := os.ReadFile("/proc/" + strconv.FormatUint(uint64(pid), 10) + "/comm")
	if err != nil {
		return ""
	}
	name := strings.TrimSpace(string(raw))
	return name
}

// jsonEscape — минимальная защита JSON-строки от кавычек и бэкслешей в
// причине ошибки (пути X-сокета содержат слеши).
func jsonEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}
