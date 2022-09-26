package alsftp

import (
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"
)

func closeIgnoreError(closer io.Closer) {
	if closer != nil {
		_ = closer.Close()
	}
}

func tcpSignalReader(readSigCh <-chan struct{}, reqCh chan<- request, tcpConn *net.TCPConn, readTimeout time.Duration, logNet bool) {
	defer close(reqCh)

	if logNet {
		defer log.Printf("TCP [%p] R --->> CLOSED", tcpConn)
	}

	for range readSigCh {
		headerBuf := make([]byte, 1+int16Bytes)
		err := tcpFill(tcpConn, headerBuf, readTimeout)
		if err != nil {
			if logNet {
				log.Printf("TCP [%p] R --->> ERROR: %v", tcpConn, err)
			}
			continue
		}

		if logNet {
			log.Printf("TCP [%p] R --->> READ: %x", tcpConn, headerBuf)
		}

		var body []byte
		bodySize := binary.BigEndian.Uint16(headerBuf[1:])
		if bodySize != 0 {
			body = make([]byte, bodySize)

			err = tcpFill(tcpConn, body, readTimeout)
			if err != nil {
				if logNet {
					log.Printf("TCP [%p] R --->> ERROR: %v", tcpConn, err)
				}
				continue
			}

			if logNet {
				log.Printf("TCP [%p] R --->> READ: %x", tcpConn, body)
			}
		}

		req := request{
			Type:    headerBuf[0],
			Content: body,
		}

		reqCh <- req
	}
}

func tcpReader(reqCh chan<- request, tcpConn *net.TCPConn, readTimeout time.Duration, logNet bool, wg *sync.WaitGroup) {
	defer wg.Done()
	defer close(reqCh)

	if logNet {
		defer log.Printf("TCP [%p] R --->> CLOSED", tcpConn)
	}

	for {
		headerBuf := make([]byte, 1+int16Bytes)
		err := tcpFill(tcpConn, headerBuf, readTimeout)
		if err != nil {
			if logNet {
				log.Printf("TCP [%p] R --->> ERROR: %v", tcpConn, err)
			}
			return
		}

		if logNet {
			log.Printf("TCP [%p] R --->> READ: %x", tcpConn, headerBuf)
		}

		var body []byte
		bodySize := binary.BigEndian.Uint16(headerBuf[1:])
		if bodySize != 0 {
			body = make([]byte, bodySize)

			err = tcpFill(tcpConn, body, readTimeout)
			if err != nil {
				if logNet {
					log.Printf("TCP [%p] R --->> ERROR: %v", tcpConn, err)
				}
				return
			}

			if logNet {
				log.Printf("TCP [%p] R --->> READ: %x", tcpConn, body)
			}
		}

		req := request{
			Type:    headerBuf[0],
			Content: body,
		}

		reqCh <- req
	}
}

func tcpOnceReader(reqCh chan<- request, tcpConn *net.TCPConn, timeout time.Duration, logNet bool) {
	defer close(reqCh)

	headerBuf := make([]byte, 1+int16Bytes)
	err := tcpFill(tcpConn, headerBuf, timeout)
	if err != nil {
		if logNet {
			log.Printf("TCP [%p] R --->> ERROR: %v", tcpConn, err)
		}
		return
	}

	var body []byte
	bodySize := binary.BigEndian.Uint16(headerBuf[1:])
	if bodySize != 0 {
		body = make([]byte, bodySize)

		err = tcpFill(tcpConn, body, timeout)
		if err != nil {
			if logNet {
				log.Printf("TCP [%p] R --->> ERROR: %v", tcpConn, err)
			}
			return
		}
	}

	if logNet {
		log.Printf("TCP [%p] R --->> READ: %x", tcpConn, append(headerBuf, body...))
	}

	req := request{
		Type:    headerBuf[0],
		Content: body,
	}

	select {
	case reqCh <- req:
	default:
	}
}

func tcpRead(tcpConn *net.TCPConn, timeout time.Duration, logNet bool) (request, error) {
	headerBuf := make([]byte, 1+int16Bytes)
	err := tcpFill(tcpConn, headerBuf, timeout)
	if err != nil {
		if logNet {
			log.Printf("TCP [%p] R --->> ERROR: %v", tcpConn, err)
		}
		return request{}, fmt.Errorf("TCP READ ERROR: %v", err)
	}

	var body []byte
	bodySize := binary.BigEndian.Uint16(headerBuf[1:])
	if bodySize != 0 {
		body = make([]byte, bodySize)

		err = tcpFill(tcpConn, body, timeout)
		if err != nil {
			if logNet {
				log.Printf("TCP [%p] R --->> ERROR: %v", tcpConn, err)
			}
			return request{}, fmt.Errorf("TCP READ ERROR: %v", err)
		}
	}

	if logNet {
		log.Printf("TCP [%p] R --->> READ: %x", tcpConn, append(headerBuf, body...))
	}

	req := request{
		Type:    headerBuf[0],
		Content: body,
	}

	return req, nil
}

func tcpWriter(dataCh <-chan []byte, tcpConn *net.TCPConn, logNet bool, wg *sync.WaitGroup) {
	defer wg.Done()

	if logNet {
		defer log.Printf("TCP [%p] W --->> CLOSED", tcpConn)
	}

	for data := range dataCh {
		_, err := tcpConn.Write(data)
		if err != nil {
			if logNet {
				log.Printf("TCP [%p] W --->> ERROR: %v", tcpConn, err)
			}
			return
		}

		if logNet {
			log.Printf("TCP [%p] W --->> WRITTEN: %x", tcpConn, data)
		}
	}
}

func tcpWrite(data []byte, tcpConn *net.TCPConn, logNet bool) error {
	_, err := tcpConn.Write(data)
	if err != nil {
		if logNet {
			log.Printf("TCP [%p] W --->> ERROR: %v", tcpConn, err)
		}
		return fmt.Errorf("TCP WRITE ERROR: %v", err)
	}

	if logNet {
		log.Printf("TCP [%p] W --->> WRITTEN: %x", tcpConn, data)
	}

	return nil
}

func tcpFill(tcpConn *net.TCPConn, dst []byte, timeout time.Duration) error {
	if len(dst) == 0 {
		return nil
	}

	var read int
	for {
		_ = tcpConn.SetReadDeadline(time.Now().Add(timeout))
		nCur, err := tcpConn.Read(dst[read:])
		if err != nil {
			return err
		}
		read += nCur
		if read == len(dst) {
			return nil
		}
	}
}

func udpWrite(data []byte, udpConn *net.UDPConn, logNet bool) error {
	_, err := udpConn.Write(data)
	if err != nil {
		if logNet {
			log.Printf("UDP [] W --->> ERROR: %v", err)
		}
		return err
	}

	if logNet {
		log.Printf("UDP [] W --->> WRITTEN: %x", data)
	}

	return nil
}

func udpReader(dataPartsCh chan<- dataPartition, udpConn *net.UDPConn, tcpConn *net.TCPConn, readTimeout time.Duration, logNet bool) {
	defer close(dataPartsCh)

	if logNet {
		defer log.Printf("UDP [%p] R ---> CLOSED", tcpConn)
	}

	buf := make([]byte, maxUdpSize)
	for {
		_ = udpConn.SetReadDeadline(time.Now().Add(readTimeout))
		n, err := udpConn.Read(buf)
		if err != nil {
			return
		}

		_ = tcpConn.SetReadDeadline(time.Now().Add(readTimeout))

		if logNet {
			log.Printf("UDP [%p] R --->> READ: %x", tcpConn, buf[:n])
		}

		if n < 1+int16Bytes {
			continue
		}

		if buf[0] != opSaveFilePart {
			continue
		}

		size := binary.BigEndian.Uint16(buf[1 : 1+int16Bytes])

		if uint16(n) < 1+int16Bytes+size {
			continue
		}

		content := make([]byte, size-int64Bytes)
		copy(content, buf[1+int16Bytes+int64Bytes:n])

		filePart := dataPartition{
			Id:      binary.BigEndian.Uint64(buf[1+int16Bytes : 1+int16Bytes+int64Bytes]),
			Content: content,
		}

		dataPartsCh <- filePart
	}
}
