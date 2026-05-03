package builder

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// logStream is a minimal multi-subscriber log fanout used while a build
// is in flight. Lines are persisted to disk so subscribers that connect
// after the build completes can replay from the on-disk log.
type logStream struct {
	mu     sync.Mutex
	file   *os.File
	subs   map[int]chan string
	nextID int
	closed bool
	path   string
	buffer []string // last 256 lines so late subscribers get context
}

const logBufferLines = 256

func newLogStream(path string) *logStream {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err == nil {
		// best effort
		_ = err
	}
	f, _ := os.Create(path)
	return &logStream{
		file: f,
		subs: map[int]chan string{},
		path: path,
	}
}

func (s *logStream) Write(p []byte) (int, error) {
	s.appendLines(string(p))
	return len(p), nil
}

func (s *logStream) logf(format string, args ...any) {
	s.appendLines(fmt.Sprintf(format+"\n", args...))
}

func (s *logStream) appendLines(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	if s.file != nil {
		_, _ = s.file.WriteString(text)
	}
	s.buffer = append(s.buffer, text)
	if len(s.buffer) > logBufferLines {
		s.buffer = s.buffer[len(s.buffer)-logBufferLines:]
	}
	for _, ch := range s.subs {
		select {
		case ch <- text:
		default:
			// Drop on backpressure to avoid stalling the build.
		}
	}
}

// subscribe returns a channel that receives every future log line plus a
// snapshot of the recent buffer, and an unsubscribe func.
func (s *logStream) subscribe() (<-chan string, func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.nextID
	s.nextID++
	ch := make(chan string, 256)
	for _, line := range s.buffer {
		ch <- line
	}
	s.subs[id] = ch
	return ch, func() {
		s.mu.Lock()
		delete(s.subs, id)
		s.mu.Unlock()
	}
}

func (s *logStream) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if s.file != nil {
		_ = s.file.Close()
	}
	for _, ch := range s.subs {
		close(ch)
	}
	s.subs = nil
}
