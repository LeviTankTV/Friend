//go:build darwin

package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// macOS-версия помощника: активное ПРИЛОЖЕНИЕ — NSWorkspace через штатный
// osascript (JavaScript for Automation), системного разрешения это не
// требует; idle — HIDIdleTime из ioreg (тоже без разрешений).
//
// Почему не dlopen/libobjc из Go: без cgo это тянет зависимость-мостик
// (purego) ради одного вызова; osascript уже на каждой macOS, вызывает тот
// же NSWorkspace и не добавляет в проект ни строчки небезопасного кода.
// osascript стартует ~100–200 мс — при интервале 4 с это незаметно.
//
// Честные границы (как в PLAN.md): заголовка окна и его границ НЕТ — их
// даёт только Accessibility-разрешение (System Events/CGWindowList), которого
// мы не просим. Серверу хватает имени приложения («Safari», «GoLand») —
// классификация окон работает, «посадка на окне» на macOS нет (Bounds пуст).
func collect(d time.Duration) int {
	// Закрытие stdin (оболочка умерла) — выходим, как на других ОСях.
	go func() {
		buf := make([]byte, 1)
		for {
			n, err := os.Stdin.Read(buf)
			if n == 0 || err != nil {
				os.Exit(0)
			}
		}
	}()

	if _, err := exec.LookPath("osascript"); err != nil {
		fmt.Println(`{"error":"focuswatch: не найден osascript — активное окно недоступно"}`)
		return 0
	}

	enc := json.NewEncoder(os.Stdout)
	was := sample{Process: "\x00"}
	nextBeat := time.Time{}
	ticker := time.NewTicker(d)
	defer ticker.Stop()

	for {
		s := current()
		if s.Process != "" {
			changed := s.Process != was.Process
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

// current — активное приложение прямо сейчас. Пустой Process — фронтмен не
// определён (ошибка osascript, экран блокировки).
func current() sample {
	s := sample{Process: frontmost(), Idle: idleSeconds()}
	if selftestMode {
		s.Process = "SelftestTarget"
		s.Title = constSelftestTitle
		s.Idle = 1
		s.Bounds = [4]int{}
	}
	return s
}

// frontmost — имя активного приложения через NSWorkspace (JXA). Вывод —
// строка без кавычек и с переводом строки на конце.
func frontmost() string {
	const script = `ObjC.import('AppKit'); ` +
		`ObjC.unwrap($.NSWorkspace.sharedWorkspace.frontmostApplication.localizedName)`
	out, err := exec.Command("osascript", "-l", "JavaScript", "-e", script).Output()
	if err != nil {
		return ""
	}
	name := strings.TrimSpace(string(out))
	name = strings.Trim(name, `"`)
	if name == "" || name == "undefined" || name == "null" {
		return ""
	}
	// Приватность та же, что на других ОСях: только имя, без пути.
	if i := strings.LastIndexAny(name, "/\\"); i >= 0 {
		name = name[i+1:]
	}
	return name
}

// idleSeconds — секунд с последнего ввода: HIDIdleTime (нано­секунды) из
// реестра устройств. Ошибка → 0 = «данных нет», сервер не выдумывает idle.
func idleSeconds() int {
	out, err := exec.Command("ioreg", "-c", "IOHIDSystem").Output()
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(out), "\n") {
		i := strings.Index(line, "HIDIdleTime")
		if i < 0 {
			continue
		}
		f := strings.Fields(line[i:])
		if len(f) < 2 {
			return 0
		}
		nanos, err := strconv.ParseFloat(strings.Trim(f[1], "="), 64)
		if err != nil || math.IsNaN(nanos) || nanos < 0 {
			return 0
		}
		return int(nanos / 1e9)
	}
	return 0
}
