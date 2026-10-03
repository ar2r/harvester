// Мок-сервис API ЛК ФНС «Мои чеки онлайн» для интеграционного тестирования.
// Отдаёт детерминированный набор чеков (см. internal/mocklkdr).
//
// Запуск: docker compose up mocklkdr, либо напрямую:
//
//	go run ./cmd/mocklkdr -addr :8080
//
// Подключение приложения: в config.json указать lkdr.apiUrl = http://127.0.0.1:8080.
package main

import (
	"flag"
	"log"
	"net/http"
	// Маршаллеры дат lkdr-api подгружают Europe/Moscow через time.LoadLocation;
	// встраиваем tzdata, чтобы мок работал в образах без /usr/share/zoneinfo.
	_ "time/tzdata"

	"github.com/ar2r/ledger-fox/internal/mocklkdr"
)

func main() {
	addr := flag.String("addr", ":8080", "адрес прослушивания")
	flag.Parse()

	log.Printf("mock lkdr listening on %s", *addr)
	if err := http.ListenAndServe(*addr, mocklkdr.New().Handler()); err != nil {
		log.Fatal(err)
	}
}
