// main.go — soundwatch: уровень звука на колонках (WASAPI loopback).
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
package main

import (
	"flag"
	"fmt"
	"os"
	"time"
)

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

	w := &wasapi{}
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
			nw := &wasapi{}
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
