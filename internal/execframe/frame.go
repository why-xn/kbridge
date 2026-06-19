// Package execframe is the length-prefixed wire format for interactive exec.
// Both the CLI and central import it so the framing has one definition.
package execframe

import (
	"encoding/binary"
	"fmt"
	"io"
)

// Type identifies a frame's channel/meaning.
type Type byte

const (
	Stdin  Type = 0x00 // CLI -> central: raw stdin bytes
	Resize Type = 0x01 // CLI -> central: rows uint16, cols uint16 (BE)
	Stdout Type = 0x10 // central -> CLI: raw output bytes
	Stderr Type = 0x11 // central -> CLI: raw stderr bytes
	Exit   Type = 0x12 // central -> CLI: exit_code int32 (BE) + optional UTF-8 error
)

// MaxPayload bounds a single frame's payload to limit memory use.
const MaxPayload = 1 << 20

// Encode writes one framed message: [type:1][len:4 BE][payload].
func Encode(w io.Writer, t Type, payload []byte) error {
	if len(payload) > MaxPayload {
		return fmt.Errorf("execframe: payload %d exceeds max %d", len(payload), MaxPayload)
	}
	var hdr [5]byte
	hdr[0] = byte(t)
	binary.BigEndian.PutUint32(hdr[1:], uint32(len(payload)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	if len(payload) == 0 {
		return nil
	}
	_, err := w.Write(payload)
	return err
}

// Decode reads exactly one framed message from r.
func Decode(r io.Reader) (Type, []byte, error) {
	var hdr [5]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(hdr[1:])
	if n > MaxPayload {
		return 0, nil, fmt.Errorf("execframe: payload %d exceeds max %d", n, MaxPayload)
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	return Type(hdr[0]), payload, nil
}

// EncodeResize / DecodeResize pack a window size into a RESIZE payload.
func EncodeResize(rows, cols uint16) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint16(b[0:], rows)
	binary.BigEndian.PutUint16(b[2:], cols)
	return b
}

func DecodeResize(p []byte) (rows, cols uint16, err error) {
	if len(p) != 4 {
		return 0, 0, fmt.Errorf("execframe: bad resize payload len %d", len(p))
	}
	return binary.BigEndian.Uint16(p[0:]), binary.BigEndian.Uint16(p[2:]), nil
}

// EncodeExit / DecodeExit pack an exit code plus optional error message.
func EncodeExit(code int32, errMsg string) []byte {
	b := make([]byte, 4+len(errMsg))
	binary.BigEndian.PutUint32(b[0:], uint32(code))
	copy(b[4:], errMsg)
	return b
}

func DecodeExit(p []byte) (code int32, errMsg string, err error) {
	if len(p) < 4 {
		return 0, "", fmt.Errorf("execframe: bad exit payload len %d", len(p))
	}
	return int32(binary.BigEndian.Uint32(p[0:])), string(p[4:]), nil
}
