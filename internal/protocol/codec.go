package protocol

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const MaxLineBytes = 8 * 1024 * 1024

// LineCodec encodes each Envelope as one JSON line. It is intentionally simple
// for the MVP and works over TCP, stdio, or test pipes.
type LineCodec struct {
	reader *bufio.Reader
	writer io.Writer
}

func NewLineCodec(r io.Reader, w io.Writer) *LineCodec {
	return &LineCodec{
		reader: bufio.NewReaderSize(r, 64*1024),
		writer: w,
	}
}

func (c *LineCodec) Read() (Envelope, error) {
	line, err := c.reader.ReadBytes('\n')
	if err != nil {
		return Envelope{}, err
	}
	if len(line) > MaxLineBytes {
		return Envelope{}, fmt.Errorf("message exceeds %d bytes", MaxLineBytes)
	}

	var msg Envelope
	if err := json.Unmarshal(line, &msg); err != nil {
		return Envelope{}, err
	}
	if msg.Version != Version {
		return Envelope{}, fmt.Errorf("unsupported protocol version %d", msg.Version)
	}
	if msg.Type == "" {
		return Envelope{}, errors.New("message type is required")
	}
	return msg, nil
}

func (c *LineCodec) Write(msg Envelope) error {
	if msg.Version == 0 {
		msg.Version = Version
	}
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	_, err = c.writer.Write(b)
	return err
}
