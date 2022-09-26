package main

import (
	"alsftp/alsftp"
	"errors"
	"flag"
	"fmt"
	"log"
	"strings"
	"time"
)

var (
	mode         string
	addr         string
	file         string
	transferPort int64
	logType      int64
	blockSize    int64
	retries      int64
	ackTimeout   int64
	retryDelay   int64
)

func init() {
	flag.StringVar(&file, "file", "", "FILE [REQUIRED]")

	flag.StringVar(&mode, "mode", "", "MODE DOWNLOAD/UPLOAD")

	flag.StringVar(&addr, "addr", "", "IP:PORT [REQUIRED]")

	flag.Int64Var(&transferPort, "transfer-port", 0, "TRANSFER PORT [REQUIRED IF UPLOAD]")

	flag.Int64Var(&logType, "log", alsftp.DefaultClientLogType,
		fmt.Sprintf("LOG TYPE (0 NO LOGS / 1 PROGRESS LOGS / 2 PROGRESS AND NETWORK LOGS) [%d])", alsftp.DefaultClientLogType))

	flag.Int64Var(&blockSize, "block", alsftp.DefaultPartitionSize,
		fmt.Sprintf("BLOCK SIZE (1-%d) bytes [%d]", alsftp.MaxPartitionSize, alsftp.DefaultPartitionSize))

	flag.Int64Var(&retries, "retry-times", alsftp.DefaultRetries,
		fmt.Sprintf("RETRIES (1-%d) [%d]", alsftp.MaxRetries, alsftp.DefaultRetries))

	flag.Int64Var(&ackTimeout, "timeout", alsftp.DefaultAcknowledgementTimeout,
		fmt.Sprintf("ACKNOWLEDGEMENT TIMEOUT (1-%d) ms [%d]", alsftp.MaxAcknowledgementTimeout, alsftp.DefaultAcknowledgementTimeout))

	flag.Int64Var(&retryDelay, "retry-delay", alsftp.DefaultRetryDelay,
		fmt.Sprintf("RETRY DELAY (0-%d) ms [%d]", alsftp.MaxRetryDelay, alsftp.DefaultRetryDelay))

	flag.Parse()
}

func main() {
	if file == "" {
		log.Fatal("-file required (file)")
	}

	if mode == "" {
		log.Fatal("-mode required (upload/download)")
	}

	if addr == "" {
		log.Fatal("-addr required (remote ip address and port)")
	}

	logProgress, logNet := logType-1 >= 0, logType-2 >= 0

	var err error

	client, err := alsftp.NewClient(addr)
	if err != nil {
		log.Fatalf("INIT ERROR: %v", err)
	}

	client.SetLogging(logProgress, logNet)

	switch strings.ToLower(mode) {
	case "download":
		err = client.Download(file)
	case "upload":
		if blockSize < 1 || blockSize > alsftp.MaxPartitionSize {
			log.Fatalf("-block should be 1-%d", alsftp.MaxPartitionSize)
		}

		if ackTimeout < 1 || ackTimeout > alsftp.MaxAcknowledgementTimeout {
			log.Fatalf("-timeout should be 1-%d", alsftp.MaxAcknowledgementTimeout)
		}

		if retries < 1 || retries > alsftp.MaxRetries {
			log.Fatalf("-retries should be 1-%d", alsftp.MaxRetries)
		}

		if retryDelay < 0 || retryDelay > 1000 {
			log.Fatalf("-retry-delay should be 0-%d", alsftp.MaxRetryDelay)
		}

		if transferPort < 1024 || retryDelay > 65535 {
			log.Fatal("-transfer-port should be 1024-65535")
		}

		log.Printf("NETWORK TCP:%s TIMEOUT %dms\n", addr, ackTimeout)
		log.Printf("NETWORK UDP:%s:%d RETRYING %d TIMES WITH DELAY %dms\n", strings.Split(addr, ":")[0], transferPort, retries, retryDelay)
		log.Printf("FILE: \"%s\" WILL BE TRANSFERRED BY BLOCKS WITH SIZE %d BYTES\n", file, blockSize)
		log.Printf("LOGGING: PROGRESS - %t NETWORK - %t\n", logProgress, logNet)

		client.SetPartitionSize(uint(blockSize))
		client.SetTransferRetries(uint(retries))
		client.SetAcknowledgementTimeout(time.Millisecond * time.Duration(ackTimeout))
		client.SetRetryDelay(time.Millisecond * time.Duration(retryDelay))

		err = client.Transfer(file, uint16(transferPort))
	default:
		err = errors.New("ERROR: INVALID MODE")
	}

	if err != nil {
		log.Fatal(err)
	}
	log.Println("SUCCESS!")
}
