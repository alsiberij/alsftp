package main

import (
	"alsftp/alsftp"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
)

var (
	port    int64
	logType int64
	dir     string
)

func init() {
	flag.Int64Var(&port, "port", 0, "PORT [REQUIRED]")
	flag.Int64Var(&logType, "log", 0,
		fmt.Sprintf("LOG TYPE (0 NO LOGS / 1 PROGRESS LOGS / 2 SERVING AND NETWORK LOGS) [%d])", alsftp.DefaultServerLogType))
	flag.StringVar(&dir, "dir", ".", "DIRECTORY FOR SAVING FILES")

	flag.Parse()
}

func main() {
	if port == 0 {
		log.Fatal("-port is required")
	}

	logServing, logNet := logType-1 >= 0, logType-2 >= 0

	log.Printf("STARTING... NETWORK: 0.0.0.0:%d, DIRECTORY: %s, LOGGING: PROGRESS - %t NETWORK - %t\n", uint16(port), dir, logServing, logNet)

	server, err := alsftp.NewServer(dir)
	if err != nil {
		log.Fatal(err)
	}

	server.SetLogging(logServing, logNet)

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt)

	go func() {
		_ = <-ch
		_ = server.Shutdown()
	}()

	server.ListenAndServe(uint16(port))
}
