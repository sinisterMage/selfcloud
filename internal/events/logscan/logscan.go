// Package logscan watches container log streams and emits `container.log`
// events when configured regexes match. The set of regexes is built
// dynamically from active EventRules whose Match.Types contains
// "container.log".
package logscan

import (
	"bufio"
	"context"
	"io"
	"regexp"
	"sync"
	"time"

	"github.com/selfcloud/selfcloud/internal/log"
	"github.com/selfcloud/selfcloud/internal/runtime/container"
	"github.com/selfcloud/selfcloud/internal/store"
)

// Emitter is the bus façade — kept narrow so we don't import the events
// package and risk a cycle.
type Emitter interface {
	Emit(ev store.EventRecord)
}

// Scanner periodically reconciles its set of follow-log goroutines with
// the desired-state container set. Each follower pipes log lines through
// the active regex set and pushes a match onto the bus.
type Scanner struct {
	st  *store.Store
	rt  container.Runtime
	bus Emitter

	mu    sync.Mutex
	known map[string]context.CancelFunc // project/name -> cancel
}

// New wires a Scanner. Run must be called to start the watch.
func New(st *store.Store, rt container.Runtime, bus Emitter) *Scanner {
	return &Scanner{
		st:    st,
		rt:    rt,
		bus:   bus,
		known: map[string]context.CancelFunc{},
	}
}

// Run blocks until ctx is cancelled. It reconciles the set of log
// followers every 5 seconds.
func (s *Scanner) Run(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	s.reconcile(ctx)
	for {
		select {
		case <-ctx.Done():
			s.shutdown()
			return
		case <-t.C:
			s.reconcile(ctx)
		}
	}
}

func (s *Scanner) reconcile(ctx context.Context) {
	cs, err := s.st.ListContainers(ctx, "")
	if err != nil {
		return
	}
	want := map[string]*store.Container{}
	for i := range cs {
		c := &cs[i]
		if c.Status.Phase != store.PhaseRunning {
			continue
		}
		want[c.Meta.Project+"/"+c.Meta.Name] = c
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Stop followers for containers that disappeared.
	for k, cancel := range s.known {
		if _, ok := want[k]; !ok {
			cancel()
			delete(s.known, k)
		}
	}
	// Start new followers.
	for k, c := range want {
		if _, ok := s.known[k]; ok {
			continue
		}
		fctx, cancel := context.WithCancel(ctx)
		s.known[k] = cancel
		go s.follow(fctx, c)
	}
}

func (s *Scanner) shutdown() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, cancel := range s.known {
		cancel()
	}
	s.known = map[string]context.CancelFunc{}
}

// follow streams logs from c until ctx is cancelled. Lines that match
// any active regex emit a container.log event with the matched line and
// captured groups.
func (s *Scanner) follow(ctx context.Context, c *store.Container) {
	pr, pw := io.Pipe()
	defer pr.Close()

	go func() {
		defer pw.Close()
		// Logs(follow=true) is best-effort: the stub returns immediately
		// with whatever it cached, the real ctr-backed runtime tails.
		if err := s.rt.Logs(ctx, c, true, pw); err != nil && ctx.Err() == nil {
			log.With("err", err, "container", c.Meta.Name).Debug("logscan: follow error")
		}
	}()

	scan := bufio.NewScanner(pr)
	scan.Buffer(make([]byte, 1<<10), 1<<20)
	for scan.Scan() {
		if ctx.Err() != nil {
			return
		}
		line := scan.Text()
		regs := s.activeRegexes(ctx, c.Meta.Project)
		for _, rg := range regs {
			groups := rg.FindStringSubmatch(line)
			if groups == nil {
				continue
			}
			data := map[string]string{
				"container": c.Meta.Name,
				"line":      line,
				"pattern":   rg.String(),
			}
			for i, name := range rg.SubexpNames() {
				if name != "" && i < len(groups) {
					data["g."+name] = groups[i]
				}
			}
			s.bus.Emit(store.EventRecord{
				Type:    "container.log",
				Project: c.Meta.Project,
				Subject: c.Meta.Name,
				Data:    data,
			})
		}
	}
}

// activeRegexes returns the union of all subjects across enabled
// EventRules in this project that match container.log. Compiled fresh
// each call to keep the implementation simple; the scan rate is much
// lower than the line rate, and bad patterns are silently skipped.
func (s *Scanner) activeRegexes(ctx context.Context, project string) []*regexp.Regexp {
	rules, err := s.st.ListEventRules(ctx, project)
	if err != nil {
		return nil
	}
	out := make([]*regexp.Regexp, 0, len(rules))
	for i := range rules {
		r := &rules[i]
		if !r.Enabled {
			continue
		}
		hit := false
		for _, t := range r.Match.Types {
			if t == "container.log" || t == "*" {
				hit = true
				break
			}
		}
		if !hit || r.Match.Subject == "" {
			continue
		}
		re, err := regexp.Compile(r.Match.Subject)
		if err != nil {
			continue
		}
		out = append(out, re)
	}
	return out
}
