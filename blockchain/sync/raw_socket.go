package sync

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

const (
	// Protocol constants
	protocolVersion   = 1
	headerSize        = 28
	maxMessageSize    = 1 << 24 // 16MB
	defaultBufferSize = 4096

	// Message types
	msgTypeTx   = 1
	msgTypePing = 2
	msgTypePong = 3
)

// MessageHeader represents a protocol message header
type MessageHeader struct {
	Version     uint32
	Type        uint32
	PayloadSize uint32
	Timestamp   int64
	Checksum    uint64
}

// RawSocket handles low-level network communication
type RawSocket struct {
	conn   net.Conn
	addr   string
	config *SyncConfig
	sendCh chan []byte
	recvCh chan []byte
	done   chan struct{}
	wg     sync.WaitGroup
	mu     sync.RWMutex
}

// NewRawSocket creates a new raw socket connection
func NewRawSocket(addr string, config *SyncConfig) (*RawSocket, error) {
	if config == nil {
		config = DefaultSyncConfig()
	}

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to peer: %v", err)
	}

	socket := &RawSocket{
		conn:   conn,
		addr:   addr,
		config: config,
		sendCh: make(chan []byte, config.SendBufferSize),
		recvCh: make(chan []byte, config.RecvBufferSize),
		done:   make(chan struct{}),
	}

	socket.wg.Add(2)
	go socket.sendLoop()
	go socket.receiveLoop()

	return socket, nil
}

// NewRawSocketFromConn creates a new raw socket from an existing connection
func NewRawSocketFromConn(conn net.Conn, config *SyncConfig) *RawSocket {
	if config == nil {
		config = DefaultSyncConfig()
	}

	socket := &RawSocket{
		conn:   conn,
		config: config,
		sendCh: make(chan []byte, config.SendBufferSize),
		recvCh: make(chan []byte, config.RecvBufferSize),
		done:   make(chan struct{}),
	}

	socket.wg.Add(2)
	go socket.sendLoop()
	go socket.receiveLoop()

	return socket
}

// Close closes the socket connection
func (s *RawSocket) Close() error {
	close(s.done)
	s.wg.Wait()
	return s.conn.Close()
}

// SendTransaction sends a transaction over the raw socket
func (s *RawSocket) SendTransaction(txData []byte, txHash string) error {
	if len(txData) > maxMessageSize {
		return fmt.Errorf("transaction size exceeds maximum allowed size")
	}

	header := MessageHeader{
		Version:     protocolVersion,
		Type:        msgTypeTx,
		PayloadSize: uint32(len(txData)),
		Timestamp:   time.Now().UTC().UnixNano(),
		Checksum:    calculateChecksum(txData),
	}

	msg := make([]byte, headerSize+len(txData))
	if err := writeHeader(msg[:headerSize], &header); err != nil {
		return fmt.Errorf("failed to write message header: %v", err)
	}

	copy(msg[headerSize:], txData)

	select {
	case s.sendCh <- msg:
		fmt.Printf("DEBUG: Sent message to channel, size: %d\n", len(msg))
		return nil
	case <-s.done:
		return fmt.Errorf("socket closed")
	case <-time.After(s.config.SendTimeout):
		return fmt.Errorf("send timeout")
	}
}

// ReceiveTransaction receives a transaction from the raw socket
func (s *RawSocket) ReceiveTransaction() ([]byte, error) {
	select {
	case data := <-s.recvCh:
		fmt.Printf("DEBUG: Received message from channel, size: %d\n", len(data))
		return data, nil
	case <-s.done:
		return nil, fmt.Errorf("socket closed")
	case <-time.After(s.config.RecvTimeout):
		return nil, fmt.Errorf("receive timeout")
	}
}

func (s *RawSocket) sendLoop() {
	defer s.wg.Done()

	for {
		select {
		case msg := <-s.sendCh:
			fmt.Printf("DEBUG: Sending message to socket, size: %d\n", len(msg))
			if err := s.writeMessage(msg); err != nil {
				fmt.Printf("DEBUG: Error sending message: %v\n", err)
				continue
			}
			fmt.Printf("DEBUG: Message sent successfully\n")
		case <-s.done:
			return
		}
	}
}

func (s *RawSocket) receiveLoop() {
	defer s.wg.Done()

	for {
		select {
		case <-s.done:
			return
		default:
			header, err := s.readHeader(nil)
			if err != nil {
				if err != io.EOF {
					fmt.Printf("DEBUG: Error reading header: %v\n", err)
				}
				continue
			}

			fmt.Printf("DEBUG: Received header, payload size: %d\n", header.PayloadSize)

			if header.PayloadSize > maxMessageSize {
				fmt.Printf("DEBUG: Message too large: %d\n", header.PayloadSize)
				continue
			}

			payload := make([]byte, header.PayloadSize)
			if err := s.readFull(payload); err != nil {
				fmt.Printf("DEBUG: Error reading payload: %v\n", err)
				continue
			}

			if calculateChecksum(payload) != header.Checksum {
				fmt.Printf("DEBUG: Checksum mismatch\n")
				continue
			}

			select {
			case s.recvCh <- payload:
				fmt.Printf("DEBUG: Message forwarded to receive channel\n")
			case <-s.done:
				return
			}
		}
	}
}

func (s *RawSocket) writeMessage(msg []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.conn.SetWriteDeadline(time.Now().Add(s.config.WriteTimeout)); err != nil {
		return err
	}

	_, err := s.conn.Write(msg)
	return err
}

func (s *RawSocket) readHeader(buf []byte) (*MessageHeader, error) {
	if err := s.conn.SetReadDeadline(time.Now().Add(s.config.ReadTimeout)); err != nil {
		return nil, err
	}

	headerBuf := make([]byte, headerSize)
	if err := s.readFull(headerBuf); err != nil {
		return nil, err
	}

	header := &MessageHeader{}
	if err := readHeader(headerBuf, header); err != nil {
		return nil, err
	}

	return header, nil
}

func (s *RawSocket) readFull(buf []byte) error {
	_, err := io.ReadFull(s.conn, buf)
	return err
}

func writeHeader(buf []byte, header *MessageHeader) error {
	if len(buf) < headerSize {
		return fmt.Errorf("buffer too small")
	}

	binary.BigEndian.PutUint32(buf[0:4], header.Version)
	binary.BigEndian.PutUint32(buf[4:8], header.Type)
	binary.BigEndian.PutUint32(buf[8:12], header.PayloadSize)
	binary.BigEndian.PutUint64(buf[12:20], uint64(header.Timestamp))
	binary.BigEndian.PutUint64(buf[20:28], header.Checksum)

	return nil
}

func readHeader(buf []byte, header *MessageHeader) error {
	if len(buf) < headerSize {
		return fmt.Errorf("buffer too small")
	}

	header.Version = binary.BigEndian.Uint32(buf[0:4])
	header.Type = binary.BigEndian.Uint32(buf[4:8])
	header.PayloadSize = binary.BigEndian.Uint32(buf[8:12])
	header.Timestamp = int64(binary.BigEndian.Uint64(buf[12:20]))
	header.Checksum = binary.BigEndian.Uint64(buf[20:28])

	return nil
}

func calculateChecksum(data []byte) uint64 {
	// Simple FNV-1a hash for demonstration
	var hash uint64 = 14695981039346656037
	for _, b := range data {
		hash ^= uint64(b)
		hash *= 1099511628211
	}
	return hash
}
