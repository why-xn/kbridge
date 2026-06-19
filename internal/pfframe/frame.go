// Package pfframe is the wire format for port-forward, layered on the shared
// execframe envelope. Connection-scoped payloads begin with a uint32 conn_id.
package pfframe

import (
	"encoding/binary"
	"fmt"
	"io"

	"github.com/why-xn/kbridge/internal/execframe"
)

// Type identifies a port-forward frame.
type Type byte

const (
	Open         Type = 0x01 // CLI->central: conn_id(4) + remote_port(2)
	Data         Type = 0x02 // both: conn_id(4) + raw bytes
	Close        Type = 0x03 // both: conn_id(4)
	ConnError    Type = 0x04 // central->CLI: conn_id(4) + UTF-8 error
	Ready        Type = 0x05 // central->CLI: no payload
	SessionError Type = 0x06 // central->CLI: UTF-8 error
)

// Encode writes one typed port-forward frame.
func Encode(w io.Writer, t Type, payload []byte) error { return execframe.WriteFrame(w, byte(t), payload) }

// Decode reads one typed port-forward frame.
func Decode(r io.Reader) (Type, []byte, error) {
	b, p, err := execframe.ReadFrame(r)
	return Type(b), p, err
}

// EncodeOpen / DecodeOpen pack an OPEN payload (conn_id + remote port).
func EncodeOpen(connID uint32, port uint16) []byte {
	b := make([]byte, 6)
	binary.BigEndian.PutUint32(b, connID)
	binary.BigEndian.PutUint16(b[4:], port)
	return b
}

func DecodeOpen(p []byte) (connID uint32, port uint16, err error) {
	if len(p) != 6 {
		return 0, 0, fmt.Errorf("pfframe: bad open payload len %d", len(p))
	}
	return binary.BigEndian.Uint32(p), binary.BigEndian.Uint16(p[4:]), nil
}

// EncodeData / DecodeData pack a DATA payload (conn_id + bytes).
func EncodeData(connID uint32, data []byte) []byte {
	b := make([]byte, 4+len(data))
	binary.BigEndian.PutUint32(b, connID)
	copy(b[4:], data)
	return b
}

func DecodeData(p []byte) (connID uint32, data []byte, err error) {
	if len(p) < 4 {
		return 0, nil, fmt.Errorf("pfframe: bad data payload len %d", len(p))
	}
	return binary.BigEndian.Uint32(p), p[4:], nil
}

// EncodeConnID / DecodeConnID pack a conn_id-only payload (CLOSE).
func EncodeConnID(connID uint32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, connID)
	return b
}

func DecodeConnID(p []byte) (uint32, error) {
	if len(p) < 4 {
		return 0, fmt.Errorf("pfframe: bad payload len %d", len(p))
	}
	return binary.BigEndian.Uint32(p), nil
}

// EncodeConnError / DecodeConnError pack a CONN_ERROR payload (conn_id + message).
func EncodeConnError(connID uint32, msg string) []byte {
	b := make([]byte, 4+len(msg))
	binary.BigEndian.PutUint32(b, connID)
	copy(b[4:], msg)
	return b
}

func DecodeConnError(p []byte) (connID uint32, msg string, err error) {
	if len(p) < 4 {
		return 0, "", fmt.Errorf("pfframe: bad conn-error payload len %d", len(p))
	}
	return binary.BigEndian.Uint32(p), string(p[4:]), nil
}
