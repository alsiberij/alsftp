package alsftp

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"time"
)

type (
	Client struct {
		addr *net.TCPAddr

		needToLogProgress bool
		needToLogNet      bool

		transferRetries    uint
		transferRetryDelay time.Duration
		transferTimeout    time.Duration
		partitionSize      uint

		tcpTimeout time.Duration
	}
)

func NewClient(address string) (*Client, error) {
	addr, err := net.ResolveTCPAddr("tcp4", address)
	if err != nil {
		return nil, err
	}
	return &Client{
		addr:            addr,
		transferRetries: DefaultRetries,
		transferTimeout: time.Millisecond * DefaultAcknowledgementTimeout,
		tcpTimeout:      time.Millisecond * DefaultTcpTimeout,
		partitionSize:   MaxPartitionSize,
	}, nil
}

func (c *Client) SetAcknowledgementTimeout(timeout time.Duration) {
	c.transferTimeout = timeout
}

func (c *Client) SetLogging(logProgress, logNetwork bool) {
	c.needToLogProgress, c.needToLogNet = logProgress, logNetwork
}

func (c *Client) SetTransferRetries(retries uint) {
	c.transferRetries = retries
}

func (c *Client) SetPartitionSize(size uint) {
	c.partitionSize = size
}

func (c *Client) SetRetryDelay(wait time.Duration) {
	c.transferRetryDelay = wait
}

func (c *Client) Transfer(filename string, transferPort uint16) error {
	dialer := net.Dialer{Timeout: time.Second}
	conn, err := dialer.Dial("tcp4", c.addr.String())
	if err != nil {
		return err
	}
	tcpConn, _ := conn.(*net.TCPConn)
	defer closeIgnoreError(tcpConn)

	err = c.requestOpenConnection(tcpConn, transferPort)
	if err != nil {
		return err
	}

	udpConn, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: c.addr.IP, Port: int(transferPort)})
	if err != nil {
		return err
	}
	defer closeIgnoreError(udpConn)

	err = c.requestSaveFileName(tcpConn, filename)
	if err != nil {
		return err
	}

	err = c.requestSendFileParts(tcpConn, udpConn, filename)
	if err != nil {
		return err
	}

	err = c.requestEOF(tcpConn)
	if err != nil {
		return err
	}

	return nil
}

func (c *Client) logNetwork(data string) {
	if c.needToLogNet {
		log.Println(data)
	}
}

func (c *Client) logProgress(data string) {
	if c.needToLogProgress {
		log.Println(data)
	}
}

func (c *Client) requestOpenConnection(tcpConn *net.TCPConn, port uint16) error {
	c.logProgress("OPENING CONNECTION...")

	buffer := make([]byte, 1+int16Bytes+int16Bytes)
	buffer[0] = opOpenConnection
	binary.BigEndian.PutUint16(buffer[1:1+int16Bytes], int16Bytes)
	binary.BigEndian.PutUint16(buffer[1+int16Bytes:1+int16Bytes+int16Bytes], port)

	err := tcpWrite(buffer, tcpConn, c.needToLogNet)
	if err != nil {
		return err
	}

	reqCh := make(chan request, 1)
	go tcpOnceReader(reqCh, tcpConn, c.tcpTimeout, c.needToLogNet)

	select {
	case <-time.After(c.tcpTimeout):
		return errors.New("ERROR: ACKNOWLEDGEMENT TIMEOUT")
	case req, ok := <-reqCh:
		if !ok {
			return errors.New("ERROR: ACKNOWLEDGEMENT CAN NOT BE RECEIVED")
		}

		if req.Type != ackOpenConnection {
			return errors.New("ERROR: UNEXPECTED ACKNOWLEDGEMENT TYPE")
		}

		if len(req.Content) != 1 {
			return errors.New("ERROR: UNEXPECTED ACKNOWLEDGEMENT CONTENT")
		}

		if req.Content[0] != ackOK {
			return errors.New("ERROR: SERVER REJECTED TO OPEN UDP CONNECTION")
		}
	}

	c.logProgress("CONFIRMED")

	return nil
}

func (c *Client) requestSaveFileName(tcpConn *net.TCPConn, filename string) error {
	c.logProgress("SAVING FILENAME...")

	buffer := make([]byte, 1+int16Bytes)
	buffer[0] = opSaveFilename
	binary.BigEndian.PutUint16(buffer[1:1+int16Bytes], uint16(len(filename)))

	err := tcpWrite(append(buffer, filename...), tcpConn, c.needToLogNet)
	if err != nil {
		return err
	}

	reqCh := make(chan request, 1)
	go tcpOnceReader(reqCh, tcpConn, c.tcpTimeout, c.needToLogNet)

	select {
	case <-time.After(c.tcpTimeout):
		return errors.New("ERROR: ACKNOWLEDGEMENT TIMEOUT")
	case req, ok := <-reqCh:
		if !ok {
			return errors.New("ERROR: ACKNOWLEDGEMENT CAN NOT BE RECEIVED")
		}

		if req.Type != ackSaveFilename {
			return errors.New("ERROR: UNEXPECTED ACKNOWLEDGEMENT TYPE")
		}

		if len(req.Content) != 1 {
			return errors.New("ERROR: UNEXPECTED ACKNOWLEDGEMENT CONTENT")
		}

		if req.Content[0] != ackOK {
			return errors.New("ERROR: SERVER REJECTED TO SAVE FILENAME")
		}
	}

	c.logProgress("CONFIRMED")

	return nil
}

func (c *Client) requestEOF(tcpConn *net.TCPConn) error {
	c.logProgress("EOF...")

	buffer := make([]byte, 1+int16Bytes)
	buffer[0] = opEOF
	binary.BigEndian.PutUint16(buffer[1:1+int16Bytes], 0)

	err := tcpWrite(buffer, tcpConn, c.needToLogNet)
	if err != nil {
		return err
	}

	reqCh := make(chan request, 1)
	go tcpOnceReader(reqCh, tcpConn, c.tcpTimeout, c.needToLogNet)

	select {
	case <-time.After(c.tcpTimeout):
		return errors.New("ERROR: ACKNOWLEDGEMENT TIMEOUT")
	case req, ok := <-reqCh:
		if !ok {
			return errors.New("ERROR: ACKNOWLEDGEMENT CAN NOT BE RECEIVED")
		}

		if req.Type != ackEOF {
			return errors.New("ERROR: UNEXPECTED ACKNOWLEDGEMENT TYPE")
		}

		if len(req.Content) != 1 {
			return errors.New("ERROR: UNEXPECTED ACKNOWLEDGEMENT CONTENT")
		}

		if req.Content[0] != ackOK {
			return errors.New("ERROR: SERVER REJECTED TO SAVE FILENAME")
		}
	}

	c.logProgress("CONFIRMED")

	return nil
}

func (c *Client) requestSendFileParts(tcpConn *net.TCPConn, udpConn *net.UDPConn, filename string) error {
	c.logProgress("FILE TRANSFERRING...")

	f, err := os.OpenFile(filename, os.O_RDONLY, 0444)
	if err != nil {
		return fmt.Errorf("FILE OPEN ERROR: %v", err)
	}
	defer closeIgnoreError(f)

	stat, err := f.Stat()
	if err != nil {
		return fmt.Errorf("FILE OPEN ERROR: %v", err)
	}

	var partId, n int
	var totalTransferred int64

	size := stat.Size()
	nthPercent := int(float64(size) / float64(c.partitionSize) / 100)
	if nthPercent == 0 {
		nthPercent = 1
	}

	readSigCh, reqCh := make(chan struct{}), make(chan request)
	go tcpSignalReader(readSigCh, reqCh, tcpConn, c.transferTimeout, c.needToLogNet)
	defer close(readSigCh)

	t := time.Now()
	buf := make([]byte, 1+int16Bytes+int64Bytes+c.partitionSize)
	for {
		n, err = f.Read(buf[1+int16Bytes+int64Bytes:])
		if err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("FILE READ ERROR: %v", err)
		}

		buf[0] = opSaveFilePart
		binary.BigEndian.PutUint16(buf[1:1+int16Bytes], uint16(int64Bytes+n))
		binary.BigEndian.PutUint64(buf[1+int16Bytes:1+int16Bytes+int64Bytes], uint64(partId))

		err = c.sendPartition(udpConn, buf, uint64(partId), readSigCh, reqCh)
		if err != nil {
			return err
		}

		partId++
		totalTransferred += int64(n)

		if partId%nthPercent == 0 {
			c.logProgress(fmt.Sprintf("PROGRESS: %d%%", int(float64(totalTransferred)/float64(size)*100)))
		}
	}

	elapsed := time.Now().Sub(t).Seconds()
	c.logProgress(fmt.Sprintf("TRANSFERRED %d BYTES IN %.6fs. SPEED ~%.0f b/s.", totalTransferred, elapsed, float64(totalTransferred)/elapsed))
	return nil
}

func (c *Client) sendPartition(udpConn *net.UDPConn, partition []byte, partitionId uint64, readSigCh chan struct{}, reqCh chan request) error {
	var transferred bool
	var retries uint

	for {
		c.logNetwork(fmt.Sprintf("TRANSFERRING PARTITION #%d ATTEMPT #%d...", partitionId, retries))

		if retries == c.transferRetries {
			return errors.New("RETRIES LIMIT EXCEEDED")
		}

		err := udpWrite(partition, udpConn, c.needToLogNet)
		if err != nil {
			return fmt.Errorf("UDP WRITE ERROR: %v", err)
		}

		select {
		case readSigCh <- struct{}{}:
			select {
			case <-time.After(c.transferTimeout):
				c.logNetwork("ERROR: TIMEOUT, RETRYING...")
				transferred = false
			case req, ok := <-reqCh:
				if !ok {
					return errors.New("ERROR: ACKNOWLEDGEMENT CAN NOT BE RECEIVED")
				}

				if req.Type != ackFilePart {
					return errors.New("ERROR: UNEXPECTED ACKNOWLEDGEMENT TYPE")
				}

				if len(req.Content) != int64Bytes+1 {
					return errors.New("ERROR: UNEXPECTED ACKNOWLEDGEMENT CONTENT")
				}

				id, statusOK := binary.BigEndian.Uint64(req.Content[:int64Bytes]), req.Content[int64Bytes] == ackOK

				if id == partitionId && statusOK {
					transferred = true
				}
			}
		default:
			transferred = false
		}

		if transferred {
			break
		}
		time.Sleep(c.transferRetryDelay)
		retries++
	}

	return nil
}

func (c *Client) Download(filename string) error {
	tcpConn, err := net.DialTCP("tcp4", nil, c.addr)
	if err != nil {
		return fmt.Errorf("RCP ERROR: %v", err)
	}
	defer closeIgnoreError(tcpConn)

	err = c.requestDownload(tcpConn, filename)
	if err != nil {
		return err
	}

	f, err := os.OpenFile(filename, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0x222)
	if err != nil {
		return fmt.Errorf("ERROR CREATING FILE: %v", err)
	}
	defer closeIgnoreError(f)

	c.logProgress("TRANSFER STARTED...")

	var req request
	for {
		req, err = tcpRead(tcpConn, c.tcpTimeout, c.needToLogNet)
		if err != nil {
			return err
		}

		if req.Type == ackDownload {
			_, err = f.Write(req.Content)
			if err != nil {
				return fmt.Errorf("FILE WRITE ERROR: %v", err)
			}
		} else if req.Type == ackEOF && req.Content[0] == ackOK {
			break
		} else {
			return errors.New("ERROR: FAILED TO RECEIVE FILE PART OR EOF")
		}
	}

	return nil
}

func (c *Client) requestDownload(tcpConn *net.TCPConn, filename string) error {
	c.logProgress("REQUESTING FILE...")

	buffer := make([]byte, 1+int16Bytes)
	buffer[0] = opDownload
	binary.BigEndian.PutUint16(buffer[1:1+int16Bytes], uint16(len(filename)))

	err := tcpWrite(append(buffer, filename...), tcpConn, c.needToLogNet)
	if err != nil {
		return err
	}

	reqCh := make(chan request, 1)
	go tcpOnceReader(reqCh, tcpConn, c.tcpTimeout, c.needToLogNet)

	select {
	case <-time.After(c.tcpTimeout):
		return errors.New("ERROR: ACKNOWLEDGEMENT TIMEOUT")
	case req, ok := <-reqCh:
		if !ok {
			return errors.New("ERROR: ACKNOWLEDGEMENT CAN NOT BE RECEIVED")
		}

		if req.Type != ackDownload {
			if req.Type == ackEOF {
				return errors.New("ERROR: SERVER HAS NO SUCH FILE")
			}
			return errors.New("ERROR: UNEXPECTED ACKNOWLEDGEMENT TYPE")
		}

		if len(req.Content) != 1 {
			return errors.New("ERROR: UNEXPECTED ACKNOWLEDGEMENT CONTENT")
		}

		if req.Content[0] != ackOK {
			return errors.New("ERROR: SERVER REJECTED TO SEND FILE")
		}
	}

	c.logProgress("CONFIRMED")

	return nil
}
