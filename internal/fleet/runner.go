package fleet

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// A running join, so the wizard can be closed and reopened without losing the
// thread. Installing Docker takes minutes, and a person should be able to go and
// do something else meanwhile.
type job struct {
	mu    sync.Mutex
	lines []Progress
	done  bool
	err   string
	subs  map[chan Progress]struct{}
}

func (j *job) emit(p Progress) {
	j.mu.Lock()
	j.lines = append(j.lines, p)
	if len(j.lines) > 2000 {
		j.lines = j.lines[len(j.lines)-2000:]
	}
	if p.Err != "" {
		j.err = p.Err
	}
	if p.Done || p.Err != "" {
		j.done = true
	}
	for ch := range j.subs {
		select {
		case ch <- p:
		default: // a slow reader must not stall the install
		}
	}
	j.mu.Unlock()
}

// fail records an error unless one has already been reported, so a failure that
// travelled up through a return value does not appear twice.
func (j *job) fail(step string, err error) {
	if j.finished() {
		return
	}
	j.emit(Progress{Step: step, Err: err.Error()})
}

func (j *job) finished() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.done
}

// Follow replays what has happened so far and then streams the rest.
func (s *Service) Follow(ctx context.Context, id string, line func(Progress)) {
	s.jobMu.Lock()
	j := s.jobs[id]
	s.jobMu.Unlock()
	if j == nil {
		return
	}
	ch := make(chan Progress, 64)
	j.mu.Lock()
	for _, p := range j.lines {
		line(p)
	}
	if j.done {
		j.mu.Unlock()
		return
	}
	j.subs[ch] = struct{}{}
	j.mu.Unlock()
	defer func() {
		j.mu.Lock()
		delete(j.subs, ch)
		j.mu.Unlock()
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case p := <-ch:
			line(p)
			if p.Done || p.Err != "" {
				return
			}
		}
	}
}

// StartJoin installs the daemon on a server and adopts it. It returns as soon as
// the work has started.
//
// The join runs on its own context rather than the request's, because closing
// the wizard must not abandon a half-finished install. That is how a server ends
// up with Docker and no daemon.
func (s *Service) StartJoin(ctx context.Context, actor, id string, creds Credentials, version string) error {
	v, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	// Refuse now rather than in the goroutine, so the wizard shows the problem
	// on the form the person is looking at instead of in a log they have to open.
	if strings.TrimSpace(creds.Password) == "" && strings.TrimSpace(creds.PrivateKey) == "" {
		return errors.New("give either a password or a private key for the first connection")
	}

	s.jobMu.Lock()
	if s.jobs == nil {
		s.jobs = map[string]*job{}
	}
	if j, ok := s.jobs[id]; ok && !j.finished() {
		s.jobMu.Unlock()
		return fmt.Errorf("that server is already being set up")
	}
	j := &job{subs: map[chan Progress]struct{}{}}
	s.jobs[id] = j
	s.jobMu.Unlock()

	kp, err := s.key(ctx)
	if err != nil {
		return err
	}
	if creds.User == "" {
		creds.User = v.SSHUser
	}
	s.setStatus(ctx, id, "joining", "")

	go func() {
		bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Minute)
		defer cancel()
		res, err := Join(bg, fmt.Sprintf("%s:%d", v.Host, v.SSHPort), creds, kp, version, j.emit)
		if err != nil {
			s.setStatus(bg, id, "failed", err.Error())
			// Join reports its own failures, at the step they happened on.
			// Repeating it here would print the same sentence twice and move the
			// wizard's marker past the step that actually broke.
			j.fail("verify", err)
			return
		}
		if err := s.saveJoin(bg, id, res); err != nil {
			s.setStatus(bg, id, "failed", err.Error())
			j.fail("verify", err)
			return
		}
		// Prove the path we will use from now on, not the one we just used to
		// install. If the tunnel does not work, the join has not worked.
		if err := s.Reachable(bg, id); err != nil {
			j.emit(Progress{Step: "verify", Err: "installed, but the panel could not be reached through the tunnel: " + err.Error()})
			return
		}
		_ = s.st.Audit(bg, actor, "fleet.join", v.Name, v.Host)
		j.emit(Progress{Step: "verify", Line: "This server is ready.", Done: true})
	}()
	return nil
}
