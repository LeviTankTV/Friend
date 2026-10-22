// Команда proactivetest — живая проверка инициативы (этап 4) и памяти
// (этап 3) без человека.
//
// Делает ровно то, что делает окно, и ждёт ровно того, что человек увидит:
// подключается к /ws, представляется и работает в одном из режимов:
//
//   - по умолчанию — сообщает «меня нет рядом N минут» (кадр focus с большим
//     idle) и ждёт её собственную реплику с proactive:true;
//   - -ask "текст" — шлёт реплику и печатает её ответ (проверка контекста:
//     строки обстановки и долгой памяти в живом ответе);
//   - -remember "текст" — шлёт «запомни: …» и ждёт подтверждения.
//
// Смысл именно живой проверки: планировщик проверен тестами «на фейковых
// часах», а здесь проверяется цепочка целиком — тик, запрос к НАСТОЯЩЕЙ
// модели, кадр reply в канале. Запуск:
//
//	go run ./cmd/proactivetest -addr 127.0.0.1:8787
//	go run ./cmd/proactivetest -addr 127.0.0.1:8787 -remember "кота зовут Барсик"
//	go run ./cmd/proactivetest -addr 127.0.0.1:8787 -ask "как зовут моего кота?"
//
// Код возврата: 0 — ожидание сбылось, 1 — нет.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/gorilla/websocket"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8787", "адрес сервера компаньона")
	idleMin := flag.Int("idle", 50, "сколько минут «не трогаю мышь» сообщить серверу (режим инициативы)")
	wait := flag.Duration("wait", 40*time.Second, "сколько ждать ответа")
	ask := flag.String("ask", "", "режим проверки контекста: текст реплики, ждём её ответ")
	remember := flag.String("remember", "", "режим памяти: что велеть запомнить (без слова «запомни:»)")
	flag.Parse()

	wsURL := "ws://" + *addr + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		fmt.Println("ОШИБКА: сервер не отвечает на", wsURL, "—", err)
		os.Exit(1)
	}
	defer conn.Close()

	send := func(v any) {
		if err := conn.WriteJSON(v); err != nil {
			fmt.Println("ОШИБКА: кадр не ушёл:", err)
			os.Exit(1)
		}
	}

	send(map[string]any{"type": "hello", "client": "proactivetest", "version": 1})

	switch {
	case *remember != "":
		// Команда памяти. Живая модель не нужна: память локальная.
		send(map[string]any{"type": "say", "text": "запомни: " + *remember})
		fmt.Printf("отправил: запомни: %s\n", *remember)
		exitOnReply(conn, *wait, "подтверждение запоминания", false)
	case *ask != "":
		send(map[string]any{"type": "say", "text": *ask})
		fmt.Printf("отправил: %s\n", *ask)
		exitOnReply(conn, *wait, "ответ", false)
	default:
		// Кадр «меня нет»: то же самое, что присылает помощник с idle.
		idle := *idleMin * 60
		send(map[string]any{
			"type": "focus", "app": "code.exe", "title": "main.go — VS Code", "idle": idle,
		})
		fmt.Printf("сообщил серверу: меня нет рядом %d минут (idle=%d с), жду инициативу до %s\n",
			*idleMin, idle, *wait)
		exitOnReply(conn, *wait, "проактивную реплику", true)
	}
}

// exitOnReply слушает канал и выходит 0, когда пришёл подходящий reply;
// 1 — по истечению срока. ТолькоProactive — требовать флага инициативы.
//
// Дедлайн ставится ОДИН раз на всю проверку: в gorilla чтение по таймауту
// ломает соединение, и повторные ReadMessage после него паникуют.
func exitOnReply(conn *websocket.Conn, wait time.Duration, what string, onlyProactive bool) {
	_ = conn.SetReadDeadline(time.Now().Add(wait))
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			fmt.Printf("ОЖИДАНИЕ: за %s нет %s\n", wait, what)
			os.Exit(1)
		}
		var m struct {
			Type      string `json:"type"`
			Text      string `json:"text"`
			Error     string `json:"error"`
			Proactive bool   `json:"proactive"`
		}
		if json.Unmarshal(raw, &m) != nil {
			continue
		}
		if m.Type == "reply_error" {
			fmt.Printf("ОШИБКА сервера: %s\n", m.Error)
			os.Exit(1)
		}
		if m.Type == "reply" && (!onlyProactive || m.Proactive) {
			fmt.Printf("ОТВЕТ (%s): %q — OK\n", what, m.Text)
			os.Exit(0)
		}
	}
}
