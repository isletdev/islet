package api

import "sync"

// StatusTakenOver is the close code a displaced terminal receives.
//
// In the 4000-4999 range, which the WebSocket spec leaves to the application.
// It exists so the panel can tell "somebody else is using this now" from "the
// connection dropped" — the first is a thing to say and stop, the second a
// thing to retry. Without it a second tab and a first tab take the session from
// each other forever, each reconnect displacing the other, which is what people
// saw as "detached" over and over.
const StatusTakenOver = 4001

// seats hands out the one live view a tmux session can usefully have.
//
// tmux attaches with -d, so a new client detaches every other one: the session
// is sized to its smallest client, and a phone left open would otherwise
// squeeze a desktop. That makes "most recent wins" the rule, and this is where
// the loser is told, rather than discovering it as a closed socket and racing
// to take the seat back.
type seats struct {
	mu   sync.Mutex
	held map[string]*seat
}

type seat struct{ displace func() }

func newSeats() *seats { return &seats{held: map[string]*seat{}} }

// take gives key to this caller, displacing whoever held it, and returns the
// release to call when the caller is done.
//
// The previous holder is displaced after the lock is dropped: displace closes a
// WebSocket, which can block on a handshake, and nothing else should wait on
// that. Release only clears the seat if this caller still holds it — otherwise
// a slow departure would evict the connection that replaced it.
func (s *seats) take(key string, displace func()) func() {
	cur := &seat{displace: displace}
	s.mu.Lock()
	prev := s.held[key]
	s.held[key] = cur
	s.mu.Unlock()
	if prev != nil {
		prev.displace()
	}
	return func() {
		s.mu.Lock()
		if s.held[key] == cur {
			delete(s.held, key)
		}
		s.mu.Unlock()
	}
}
