//go:build !windows && !linux

package main

import "fmt"

// macOS и прочие ОСи: системного loopback-захвата колонок у macOS нет
// (Core Audio не отдаёт звук устройств без стороннего виртуального
// драйвера). Помощник честно называет причину и выходит — оболочка не
// танцует, окно персонажа живёт как жилило. На Windows и Linux есть свои
// реализации (wasapi_windows.go / pulse_linux.go).
type noLoopback struct{}

// newSource — источник звука на этой ОСи (см. main.go, interface source).
func newSource() source { return &noLoopback{} }

func (n *noLoopback) initLoopback() error {
	return fmt.Errorf("системного loopback на этой ОСи нет (Windows: WASAPI, Linux: PulseAudio monitor)")
}

func (n *noLoopback) level() (float64, bool, error) {
	return 0, false, fmt.Errorf("системного loopback на этой ОСи нет")
}
