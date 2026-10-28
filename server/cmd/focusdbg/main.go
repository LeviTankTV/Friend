// focusdbg — диагностика X11-цепочки помощника (почему CI-проверка не видит
// активное окно): печатает каждый шаг — connect, активное окно, pid, класс,
// имя, простой. Инструмент разработчика: на живой сессии X запусти и сравни
// с тем, что показывает xprop.
//
// Каждая строка помечена временем с старта (+Nms): на CI-xvfb помощник
// зависал молча, и тайминг шагов показывает, на каком именно шаге обрывается
// цепочка (само рукопожатие подробно трассирует пакет x11 в stderr).
package main

import (
	"fmt"
	"os"
	"time"

	"friend/internal/x11"
)

func main() {
	t0 := time.Now()
	log := func(a ...any) {
		fmt.Printf("+%5dms %s\n", time.Since(t0).Milliseconds(), fmt.Sprint(a...))
	}
	log("DISPLAY=", os.Getenv("DISPLAY"), "XAUTHORITY=", os.Getenv("XAUTHORITY"))
	// Зонд формата Xauthority: разбор в Go дал «обрыв» на файле от xvfb-run,
	// сырые байты первого рекорда покажут настоящий формат длин строк.
	if p := os.Getenv("XAUTHORITY"); p != "" {
		if raw, err := os.ReadFile(p); err != nil {
			log("xauth dump: ОШИБКА:", err)
		} else {
			n := min(len(raw), 96)
			log("xauth dump: size=", len(raw), " head=", fmt.Sprintf("%x", raw[:n]))
		}
	}
	cl, err := x11.Connect()
	if err != nil {
		log("connect: ОШИБКА:", err)
		return
	}
	log("connect: ok root=", fmt.Sprintf("%x", cl.Root()))
	w, err := cl.ActiveWindow()
	log("active:", w, "err:", err)
	if w == 0 {
		return
	}
	log("pid:", cl.WindowPID(w))
	log("class:", cl.WindowClass(w))
	log("name:", cl.WindowName(w))
	log("idle:", cl.IdleSeconds())
	x, y, ww, hh := cl.WindowGeometry(w)
	log("geometry:", x, y, ww, hh)
}
