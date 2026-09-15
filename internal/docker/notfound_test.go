package docker

import (
	"errors"
	"testing"

	"github.com/isletdev/islet/internal/cmdrun"
)

// The exit code is 1 whether the daemon is down or the name was wrong, so the
// only thing to go on is what the CLI wrote. Getting this wrong in either
// direction is costly: a missing container reported as a broken daemon sends
// somebody looking for a fault that is not there, and a broken daemon reported
// as a missing container hides a real outage.
func TestIsNotFoundReadsWhatDockerActuallySays(t *testing.T) {
	cases := []struct {
		name, stderr string
		want         bool
	}{
		{"inspect a container that is gone", "Error: No such object: portainer", true},
		{"inspect by the older wording", "Error response from daemon: No such container: shop", true},
		{"a missing image", "Error: No such image: ghcr.io/me/app:v2", true},
		{"the daemon is not running", "Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?", false},
		{"permission denied on the socket", "permission denied while trying to connect to the Docker daemon socket", false},
		{"a container that will not start", "Error response from daemon: driver failed programming external connectivity", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := &cmdrun.Error{Cmd: "docker inspect", Result: cmdrun.Result{ExitCode: 1, Stderr: c.stderr}}
			if got := IsNotFound(err); got != c.want {
				t.Errorf("IsNotFound(%q) = %v, want %v", c.stderr, got, c.want)
			}
		})
	}
	if IsNotFound(errors.New("something else entirely")) {
		t.Error("an error that is not a command failure is not a missing object")
	}
	if !IsNotFound(ErrNotFound) {
		t.Error("the sentinel itself has to answer true")
	}
}
