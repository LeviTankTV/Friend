//go:build !windows && !linux && !darwin

package main

import (
	"fmt"
	"time"
)

// Прочие ОСи (не Windows/Linux/macOS): помощник честно молчит — сборка
// проекта на любой системе остаётся зелёной. Три большие платформы имеют
// свои реализации: collect_windows.go / collect_linux.go / collect_darwin.go.
func collect(_ time.Duration) int {
	fmt.Println(`{"error":"focuswatch: отслеживание активного окна есть на Windows, Linux (X11) и macOS"}`)
	return 0
}
