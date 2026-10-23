//go:build !windows && !linux

package main

import (
	"fmt"
	"time"
)

// На macOS помощник пока не работает: активное окно — другие API
// (NSWorkspace/CGWindowList) и другие разрешения. Сборку проекта это не
// ломает, окно работает без «понимания экрана» (сервер просто не получает
// кадров focus). На Windows и Linux есть свои реализации
// (collect_windows.go / collect_linux.go).
func collect(_ time.Duration) int {
	fmt.Println(`{"error":"focuswatch: отслеживание активного окна есть на Windows и Linux (X11); на macOS пока нет"}`)
	return 0
}
