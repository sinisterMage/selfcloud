// Package protocol defines the wire format used between the selfcloud
// firecracker host (Jailer) and the in-guest agent over an AF_VSOCK
// connection. Each message is a single envelope:
//
//	<u32 BE length><N bytes JSON envelope>
//
// The JSON envelope embeds the body as base64 to keep the protocol simple
// and make every field 7-bit safe. Both Request and Response are framed the
// same way; the guest reads exactly one Request per connection and writes
// exactly one Response, then closes.
package protocol

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// VsockPort is the port the agent listens on inside the guest.
const VsockPort = 5252

// MaxMessageSize bounds a single message at 32 MiB to prevent runaway
// allocations on either side.
const MaxMessageSize = 32 << 20

// Request is sent host -> guest.
type Request struct {
	Method  string              `json:"method"`
	Path    string              `json:"path"`
	Headers map[string][]string `json:"headers,omitempty"`
	Body    []byte              `json:"body,omitempty"`
	Env     map[string]string   `json:"env,omitempty"`
	// Mode mirrors the function manifest: "stdio" (default) writes the
	// request body to the child's stdin and reads the response body from
	// stdout; "http" forwards via 127.0.0.1:8080.
	Mode string `json:"mode,omitempty"`
}

// Response is sent guest -> host.
type Response struct {
	Status  int                 `json:"status"`
	Headers map[string][]string `json:"headers,omitempty"`
	Body    []byte              `json:"body,omitempty"`
	Logs    string              `json:"logs,omitempty"`
	Error   string              `json:"error,omitempty"`
}

// WriteFrame writes a length-prefixed JSON message to w.
func WriteFrame(w io.Writer, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(data) > MaxMessageSize {
		return fmt.Errorf("frame too large: %d bytes", len(data))
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(data)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

// ReadFrame reads a length-prefixed JSON message into v.
func ReadFrame(r io.Reader, v any) error {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n == 0 {
		return errors.New("empty frame")
	}
	if int(n) > MaxMessageSize {
		return fmt.Errorf("frame too large: %d bytes", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return err
	}
	return json.Unmarshal(buf, v)
}
