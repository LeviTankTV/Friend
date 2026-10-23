// main.go — soundwatch: уровень звука на колонках.
//
// Печатает в stdout по JSON-строке раз в -interval: {"level":0.0123}.
// Оболочка читает строки и передаёт странице (js/music.js) — по ним она
// решает, «играет музыка» или нет. Из сигнала живёт РОВНО RMS-число:
// содержимое звука не сохраняется, не передаётся и не покидает машину
// (строка летит только в локальную страницу, наружу — ничего).
//
// Помощник отдельным процессом (тот же приём, что у focuswatch): запросы к
// системному API должны стоить копейки и не мешать ни кадрам персонажа, ни
// серверу. Живёт, пока открыт stdin (закрылся — умерла оболочка-родитель).
//
// Источник звука — платформенный (source): WASAPI-петля на Windows,
// монитор PulseAudio на Linux, честный отказ на macOS (системного loopback
// там нет). Реализация выбирается на своей ОСи, контракт один.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"time"
)

// source — платформенный источник уровня системного звука.
type source interface {
	// initLoopback поднимает захват; на платформе без системного loopback —
	// честная ошибка, помощник выходит с причиной.
	initLoopback() error
	// level — текущий RMS 0..1. got=false — новых данных с прошлого вызова
	// не было (печатаем прошлое значение), error — захват умер, надо
	// переподключаться.
	level() (float64, bool, error)
}

func killOnStdinClose() {
	b := make([]byte, 1)
	go func() {
		for {
			if n, _ := os.Stdin.Read(b); n <= 0 {
				os.Exit(0)
			}
		}
	}()
}

func main() {
	interval := flag.Duration("interval", 200*time.Millisecond,
		"с какой периодичностью печатать строку уровня")
	flag.Parse()

	killOnStdinClose()

	w := newSource()
	if err := w.initLoopback(); err != nil {
		fmt.Fprintln(os.Stderr, "soundwatch:", err)
		os.Exit(1)
	}

	ticker := time.NewTicker(*interval)
	defer ticker.Stop()
	last := 0.0
	for range ticker.C {
		v, got, err := w.level()
		if err != nil {
			// Устройство сменилось/исчезло — не умираем: печатаем ноль и
			// пробуем переподключиться (оболочка не должна терять танцы из-за
			// смены колонок на наушники).
			fmt.Fprintln(os.Stderr, "soundwatch:", err)
			nw := newSource()
			if e := nw.initLoopback(); e == nil {
				w = nw
			}
			last = 0
			printLevel(last)
			continue
		}
		if got {
			last = v
		}
		printLevel(last)
	}
}

// printLevel — JSON-строка уровня; строку читает оболочка и передаёт странице.
func printLevel(v float64) {
	b, _ := json.Marshal(map[string]float64{"level": math.Round(v*10000) / 10000})
	os.Stdout.Write(append(b, '\n'))
}
