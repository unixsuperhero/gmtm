package tuios

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// Frame types on a stream-pane connection.
const (
	FrameSnapshot byte = 'S'
	FrameOutput   byte = 'O'
	FrameResize   byte = 'R'
	FrameError    byte = 'E'
	FrameExit     byte = 'X'
	FrameInput    byte = 'I'
	FrameLease    byte = 'L'
)

// MaxInputFrame is the daemon's cap on one I frame; longer input is split.
const MaxInputFrame = 64 * 1024

// maxFrame guards against a corrupt length prefix.
const maxFrame = 64 << 20

var timeZero time.Time

// StreamInfo is the stream-pane reply line.
type StreamInfo struct {
	BootID    string `json:"boot_id"`
	Cols      int    `json:"cols"`
	Rows      int    `json:"rows"`
	Mode      string `json:"mode"`
	Seq       uint64 `json:"seq"`
	Session   string `json:"session"`
	SessionID string `json:"session_id"`
	Title     string `json:"title"`
	Window    string `json:"window"`
}

// PaneStream is the concrete Stream returned by StreamPane.
type PaneStream struct {
	conn   net.Conn
	info   StreamInfo
	frames chan Frame
	wmu    sync.Mutex
	once   sync.Once
	err    error
	errMu  sync.Mutex
}

// Info returns the reply the daemon sent when the stream opened.
func (s *PaneStream) Info() StreamInfo { return s.info }

// Frames delivers daemon frames; it closes when the connection ends.
func (s *PaneStream) Frames() <-chan Frame { return s.frames }

// Err reports why the frame reader stopped (nil while running or after a clean Close).
func (s *PaneStream) Err() error {
	s.errMu.Lock()
	defer s.errMu.Unlock()
	return s.err
}

// SendInput writes bytes as one or more I frames.
func (s *PaneStream) SendInput(ctx context.Context, b []byte) error {
	for len(b) > 0 {
		n := min(len(b), MaxInputFrame)
		if err := s.writeFrame(ctx, FrameInput, b[:n]); err != nil {
			return err
		}
		b = b[n:]
	}
	return nil
}

// Lease sends an L frame; 0,0 releases. The caller renews it every 10s.
func (s *PaneStream) Lease(ctx context.Context, cols, rows uint16) error {
	var p [4]byte
	binary.BigEndian.PutUint16(p[0:2], cols)
	binary.BigEndian.PutUint16(p[2:4], rows)
	return s.writeFrame(ctx, FrameLease, p[:])
}

// Close ends the stream. It is safe to call more than once.
func (s *PaneStream) Close() error {
	var err error
	s.once.Do(func() { err = s.conn.Close() })
	return err
}

func (s *PaneStream) writeFrame(ctx context.Context, typ byte, payload []byte) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if dl, ok := ctx.Deadline(); ok {
		_ = s.conn.SetWriteDeadline(dl)
		defer s.conn.SetWriteDeadline(timeZero)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	buf := make([]byte, 5+len(payload))
	buf[0] = typ
	binary.BigEndian.PutUint32(buf[1:5], uint32(len(payload)))
	copy(buf[5:], payload)
	_, err := s.conn.Write(buf)
	return err
}

// StreamPane opens a dedicated stream-pane connection. Pass 0,0 for no lease
// (preview must). The stream closes when ctx ends or Close is called.
func (c *client) StreamPane(ctx context.Context, session, window string, leaseCols, leaseRows uint16) (Stream, error) {
	if window == "" {
		return nil, errors.New("tuios: stream-pane needs a window")
	}
	params := map[string]any{"window": window}
	if session != "" {
		params["session"] = session
	}
	if leaseCols != 0 || leaseRows != 0 {
		params["lease_cols"] = leaseCols
		params["lease_rows"] = leaseRows
	}
	var info StreamInfo
	rc, err := c.openDedicated(ctx, "stream-pane", params, &info)
	if err != nil {
		return nil, err
	}
	s := &PaneStream{conn: rc.c, info: info, frames: make(chan Frame, 64)}
	stop := context.AfterFunc(ctx, func() { s.Close() })
	go func() {
		defer close(s.frames)
		defer stop()
		defer s.Close()
		for {
			f, err := ReadFrame(rc.r)
			if err != nil {
				if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
					s.errMu.Lock()
					s.err = err
					s.errMu.Unlock()
				}
				return
			}
			select {
			case s.frames <- f:
			case <-ctx.Done():
				return
			}
		}
	}()
	return s, nil
}

// ReadFrame decodes one daemon frame. Unknown types are returned with the raw payload in Data.
func ReadFrame(r *bufio.Reader) (Frame, error) {
	var hdr [5]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return Frame{}, err
	}
	n := binary.BigEndian.Uint32(hdr[1:5])
	if n > maxFrame {
		return Frame{}, fmt.Errorf("tuios: frame %q too large (%d bytes)", hdr[0], n)
	}
	p := make([]byte, n)
	if _, err := io.ReadFull(r, p); err != nil {
		return Frame{}, err
	}
	f := Frame{Type: hdr[0]}
	short := func(want int) error {
		if len(p) < want {
			return fmt.Errorf("tuios: frame %q payload %d < %d", f.Type, len(p), want)
		}
		return nil
	}
	switch f.Type {
	case FrameSnapshot, FrameResize:
		if err := short(12); err != nil {
			return f, err
		}
		f.Seq = binary.BigEndian.Uint64(p[0:8])
		f.Cols = binary.BigEndian.Uint16(p[8:10])
		f.Rows = binary.BigEndian.Uint16(p[10:12])
		f.Data = p[12:]
	case FrameOutput:
		if err := short(8); err != nil {
			return f, err
		}
		f.Seq = binary.BigEndian.Uint64(p[0:8])
		f.Data = p[8:]
	default:
		f.Data = p
	}
	return f, nil
}
