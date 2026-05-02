package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"nhooyr.io/websocket"
)

// upgradeWS upgrades an HTTP request to a WebSocket connection. We allow
// any origin because the dashboard, CLI, and Terraform provider may be
// served from anywhere; auth is handled at the request layer above us.
func upgradeWS(w http.ResponseWriter, r *http.Request) (*websocket.Conn, error) {
	return websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
		Subprotocols:       []string{"selfcloud.v1"},
	})
}

// pump copies bytes from r to a WebSocket connection, framing each chunk
// as a binary message. Returns when r returns EOF or ctx is done.
func pump(ctx context.Context, conn *websocket.Conn, r io.Reader) error {
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if werr := conn.Write(ctx, websocket.MessageBinary, buf[:n]); werr != nil {
				return werr
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

// readPump copies WebSocket messages into w until ctx is done or the peer
// closes.
func readPump(ctx context.Context, conn *websocket.Conn, w io.Writer) error {
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return err
		}
		if _, err := w.Write(data); err != nil {
			return err
		}
	}
}

// pingLoop keeps the WS alive across NAT/idle proxies.
func pingLoop(ctx context.Context, conn *websocket.Conn) {
	t := time.NewTicker(20 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			_ = conn.Ping(pingCtx)
			cancel()
		}
	}
}
