//go:build !windows

package main

import (
	"fmt"
	"time"
)

// На Linux/macOS помощник не работает: там активное окно — другие API и
// другие разрешения. Сборку проекта это не ломает, окно работает без
// «понимания экрана» (сервер просто не получает кадров focus).
func collect(_ time.Duration) int {
	fmt.Println(`{"error":"focuswatch: отслеживание активного окна есть только на Windows"}`)
	return 0
}
