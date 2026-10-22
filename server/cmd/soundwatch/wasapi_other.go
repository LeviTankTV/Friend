//go:build !windows

package main

import "fmt"

// Не-Windows: loopback микшера — это Windows API; на других системах
// помощник сообщает причину и выходит (оболочка не танцует).
type wasapi struct{}

func (w *wasapi) initLoopback() error {
	return fmt.Errorf("wasapi loopback есть только на Windows")
}

func (w *wasapi) level() (float64, bool, error) {
	return 0, false, fmt.Errorf("wasapi loopback есть только на Windows")
}

func printLevel(v float64) {}
