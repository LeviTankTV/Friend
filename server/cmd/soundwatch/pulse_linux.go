//go:build linux

package main

import (
	"encoding/binary"
	"math"
	"sync"

	"friend/internal/pulse"
)

// pulseSrc — уровень звука на колонках через источник-монитор PulseAudio
// (у PipeWire тот же путь через pipewire-pulse). Контракт тот же, что у
// WASAPI-петли Windows: initLoopback → level() раз в тик.
//
// RMS считается в горутине-читателе (мемблоки приходят своим темпом,
// ~10 раз в секунду при fragsize 100 мс), level() забирает накопленное.
type pulseSrc struct {
	mu     sync.Mutex
	conn   *pulse.Conn
	stream *pulse.Stream
	sumSq  float64
	n      int
	err    error
}

// newSource — источник звука на этой ОСи (см. main.go, interface source).
func newSource() source { return &pulseSrc{} }

func (p *pulseSrc) initLoopback() error {
	conn, err := pulse.Connect()
	if err != nil {
		return err
	}
	monitor, err := conn.DefaultMonitor()
	if err != nil {
		conn.Close()
		return err
	}
	st, err := conn.Record(monitor)
	if err != nil {
		conn.Close()
		return err
	}
	st.Start()
	p.conn, p.stream, p.sumSq, p.n, p.err = conn, st, 0, 0, nil
	go p.readLoop()
	return nil
}

// readLoop читает мемблоки до первой ошибки (сервер умер, поток убит,
// сменился sink): сокет закрывается здесь же — main по ошибке level()
// поднимет новый источник, старый не должен течь файндескриптором.
func (p *pulseSrc) readLoop() {
	for {
		block, err := p.stream.Next()
		p.mu.Lock()
		if err != nil {
			p.err = err
			p.conn.Close()
			p.mu.Unlock()
			return
		}
		// PCM s16le mono: сразу сворачиваем в сумму квадратов, байты
		// не копим — приватность та же, что у WASAPI-петли.
		for i := 0; i+1 < len(block); i += 2 {
			v := float64(int16(binary.LittleEndian.Uint16(block[i : i+2])))
			p.sumSq += v * v
			p.n++
		}
		p.mu.Unlock()
	}
}

func (p *pulseSrc) level() (float64, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return 0, false, p.err
	}
	if p.n == 0 {
		return 0, false, nil
	}
	v := math.Sqrt(p.sumSq/float64(p.n)) / 32768
	p.sumSq, p.n = 0, 0
	return v, true, nil
}
