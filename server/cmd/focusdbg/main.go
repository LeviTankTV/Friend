// focusdbg — диагностика X11-цепочки помощника (почему CI-проверка не видит
// активное окно): печатает каждый шаг — connect, активное окно, pid, класс,
// имя, простой. Инструмент разработчика: на живой сессии X запусти и сравни
// с тем, что показывает xprop.
package main

import (
	"fmt"

	"friend/internal/x11"
)

func main() {
	cl, err := x11.Connect()
	if err != nil {
		fmt.Println("connect: ОШИБКА:", err)
		return
	}
	fmt.Println("connect: ok")
	w, err := cl.ActiveWindow()
	fmt.Println("active:", w, "err:", err)
	if w == 0 {
		return
	}
	fmt.Println("pid:", cl.WindowPID(w))
	fmt.Println("class:", cl.WindowClass(w))
	fmt.Println("name:", cl.WindowName(w))
	fmt.Println("idle:", cl.IdleSeconds())
	x, y, ww, hh := cl.WindowGeometry(w)
	fmt.Println("geometry:", x, y, ww, hh)
}
