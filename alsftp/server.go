package alsftp

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"sync"
	"time"
)

type (
	Server struct {
		lis net.Listener

		serverWg *sync.WaitGroup

		needToLogUserServing bool
		needToLogNet         bool

		netTimeout time.Duration

		dir string
	}

	connData struct {
		File    **os.File
		UdpConn *net.UDPConn
	}
)

func NewServer(directory string) (*Server, error) {
	d, err := os.Stat(directory)
	if err != nil {
		return nil, fmt.Errorf("DIRECTORY OPEN ERROR: %v", err)
	}

	if !d.IsDir() {
		return nil, errors.New("SPECIFIED DIRECTORY IS A FILE")
	}

	return &Server{
		dir:        directory,
		netTimeout: time.Millisecond * DefaultTcpTimeout,
		serverWg:   &sync.WaitGroup{},
	}, nil
}

func (s *Server) SetLogging(logUserServing, logNetwork bool) {
	s.needToLogUserServing, s.needToLogNet = logUserServing, logNetwork
}

func (s *Server) ListenAndServe(port uint16) {
	lis, err := net.ListenTCP("tcp4", &net.TCPAddr{Port: int(port)})
	if err != nil {
		s.logNetwork(fmt.Sprintf("LISTENER ERROR: %v", err))
		return
	}
	s.lis = lis

	var tcpConn *net.TCPConn

	for {
		tcpConn, err = lis.AcceptTCP()
		if err != nil {
			break
		}

		s.logNetwork(fmt.Sprintf("CON [%p] ACCEPTED", tcpConn))

		tcpDataCh := make(chan []byte)
		reqCh := make(chan request)

		s.serverWg.Add(3)

		go tcpReader(reqCh, tcpConn, s.netTimeout, s.needToLogNet, s.serverWg)
		go tcpWriter(tcpDataCh, tcpConn, s.needToLogNet, s.serverWg)
		go s.requestHandler(&connData{File: new(*os.File)}, tcpConn, reqCh, tcpDataCh)
	}

	s.serverWg.Wait()

	log.Println("SERVER STOPPED")
}

func (s *Server) Shutdown() error {
	if s.lis == nil {
		return errors.New("NO LISTENER")
	}

	log.Println("SHUTTING DOWN...")
	err := s.lis.Close()
	return err
}

func (s *Server) logServing(data string) {
	if s.needToLogUserServing {
		log.Println(data)
	}
}

func (s *Server) logNetwork(data string) {
	if s.needToLogNet {
		log.Println(data)
	}
}

func (s *Server) requestHandler(cd *connData, tcpConn *net.TCPConn, reqCh <-chan request, tcpDataCh chan<- []byte) {
	defer s.serverWg.Done()
	defer close(tcpDataCh)

	defer s.logServing(fmt.Sprintf("REQ [%p] H --->> CLOSED", tcpConn))

	closeSigCh := make(chan struct{})
	defer close(closeSigCh)

	for req := range reqCh {
		var err error

		s.logServing(fmt.Sprintf("REQ [%p] H --->> NEW REQUEST:", tcpConn))

		response := make([]byte, 1+int16Bytes+1)
		binary.BigEndian.PutUint16(response[1:1+int16Bytes], 1)

		switch req.Type {
		case opOpenConnection:
			response[0] = ackOpenConnection
			var udpConn *net.UDPConn

			udpConn, err = s.requestOpenConnection(req)
			if err == nil {
				cd.UdpConn = udpConn
				dataPartitionsCh := make(chan dataPartition, 1)
				tcpDataCh2 := make(chan []byte, 1)

				s.serverWg.Add(1)

				go tcpWriter(tcpDataCh2, tcpConn, s.needToLogNet, s.serverWg)

				go udpReader(dataPartitionsCh, udpConn, tcpConn, s.netTimeout, s.needToLogNet)
				go s.filePartsHandler(tcpConn, cd.File, dataPartitionsCh, tcpDataCh2)

				go func(ch chan struct{}, udpConn *net.UDPConn) {
					<-ch
					_ = udpConn.Close()
				}(closeSigCh, udpConn)
			}
		case opSaveFilename:
			response[0] = ackSaveFilename
			var f *os.File

			f, err = s.requestSaveFilename(req)
			if err == nil {
				*cd.File = f
			}

			go func(ch chan struct{}, file *os.File) {
				<-ch
				_ = file.Close()
			}(closeSigCh, f)
		case opEOF:
			response[0] = ackEOF

			closeIgnoreError(cd.UdpConn)
			err = s.requestEOF(req, cd.File)
		case opDownload:
			response[0] = ackEOF
			err = s.download(req.Content, tcpDataCh)
		}

		if err != nil {
			response[1+int16Bytes] = 0xEE
			s.logServing(fmt.Sprintf("ERROR: %v", err))
		} else {
			response[1+int16Bytes] = ackOK
			s.logServing("OK")
		}

		tcpDataCh <- response
	}
}

func (s *Server) requestOpenConnection(req request) (*net.UDPConn, error) {
	s.logServing("OPEN CONNECTION...")

	if len(req.Content) != int16Bytes {
		return nil, errors.New("UNEXPECTED BODY")
	}
	port := binary.BigEndian.Uint16(req.Content)

	udpConn, err := net.ListenUDP("udp4", &net.UDPAddr{Port: int(port)})
	if err != nil {
		return nil, err
	}

	return udpConn, nil
}

func (s *Server) requestSaveFilename(req request) (*os.File, error) {
	s.logServing("SAVE FILENAME...")

	if len(req.Content) == 0 {
		return nil, errors.New("UNEXPECTED BODY")
	}
	filename := string(req.Content)

	f, err := os.OpenFile(s.dir+"/"+filename, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0777)
	if err != nil {
		return nil, err
	}

	return f, nil
}

func (s *Server) requestEOF(req request, f **os.File) error {
	s.logServing("SAVE FILE...")

	if len(req.Content) != 0 {
		return errors.New("UNEXPECTED BODY")
	}

	if *f == nil {
		return errors.New("NO FILE TO SAVE")
	}

	err := (*f).Close()
	if err != nil {
		return err
	}

	return nil
}

func (s *Server) download(filename []byte, tcpDataCh chan<- []byte) error {
	f, err := os.OpenFile(s.dir+"/"+string(filename), os.O_RDONLY, 0444)
	if err != nil {
		return err
	}
	defer closeIgnoreError(f)

	response := make([]byte, 1+int16Bytes+1)
	response[0] = ackDownload
	binary.BigEndian.PutUint16(response[1:1+int16Bytes], uint16(1))
	response[1+int16Bytes] = 0xFF
	tcpDataCh <- response

	var n int
	header := make([]byte, 1+int16Bytes)
	buf := make([]byte, MaxPartitionSize)
	for {
		n, err = f.Read(buf)
		if err != nil {
			if err == io.EOF {
				break
			}
			return err
		}

		header[0] = ackDownload
		binary.BigEndian.PutUint16(header[1:1+int16Bytes], uint16(n))

		tcpDataCh <- append(header, buf[:n]...)
	}

	return nil
}

func (s *Server) filePartsHandler(tcpConn *net.TCPConn, f **os.File, dataPartitionsCh <-chan dataPartition, tcpDataCh chan<- []byte) {
	defer close(tcpDataCh)

	defer s.logServing(fmt.Sprintf("FIL [%p] H --->> CLOSED", tcpConn))

	for partition := range dataPartitionsCh {
		s.logServing("SAVE FILE PART...")

		if *f == nil {
			return
		}

		acknowledgment := make([]byte, 1+int16Bytes+int64Bytes+1)
		acknowledgment[0] = ackFilePart
		binary.BigEndian.PutUint16(acknowledgment[1:1+int16Bytes], int64Bytes+1)
		binary.BigEndian.PutUint64(acknowledgment[1+int16Bytes:1+int16Bytes+int64Bytes], partition.Id)

		if len(partition.Content) == 0 {
			acknowledgment[1+int16Bytes+int64Bytes] = 0xE0
		}

		_, err := (*f).Write(partition.Content)
		if err != nil {
			s.logServing(fmt.Sprintf("ERROR: %v", err))
			acknowledgment[1+int16Bytes+int64Bytes] = 0xE1
		}

		acknowledgment[1+int16Bytes+int64Bytes] = ackOK
		s.logServing(fmt.Sprintf("OK: SENDING ACKNOWLEDGEMNT FOR #%d PART", partition.Id))

		tcpDataCh <- acknowledgment
	}
}
