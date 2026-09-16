package runner

import (
	"slices"
	"strings"
	"testing"
)

// The deploy workflow this package generates tells people to reach the daemon
// at host.docker.internal. Docker only provides that name on Desktop, so on the
// Linux box Islet actually runs on, a runner container resolves it only if it
// is told to — and without that the first curl of the first job fails.
func TestRunnerContainerCanReachTheHost(t *testing.T) {
	args := containerArgs(&Pool{ID: "p1", Name: "ci"}, "islet-ci-1")
	i := slices.Index(args, "--add-host")
	if i < 0 || i+1 >= len(args) || args[i+1] != "host.docker.internal:host-gateway" {
		t.Fatalf("a runner cannot reach the daemon on its own host:\n%v", args)
	}
	if wf := DeployWorkflow("shop", "main", "islet-runner"); !strings.Contains(wf, "host.docker.internal") {
		t.Fatal("the generated workflow stopped naming the address the container is given")
	}
}

func TestRunnerLimitsAreOptional(t *testing.T) {
	plain := containerArgs(&Pool{ID: "p1"}, "n")
	if slices.Contains(plain, "--memory") || slices.Contains(plain, "--cpus") || slices.Contains(plain, "-v") {
		t.Fatalf("an unlimited pool asked docker for limits:\n%v", plain)
	}
	full := containerArgs(&Pool{ID: "p1", MemoryMB: 512, CPUs: 1.5, DockerAccess: true}, "n")
	for _, want := range []string{"--memory", "512m", "--cpus", "1.5", "/var/run/docker.sock:/var/run/docker.sock"} {
		if !slices.Contains(full, want) {
			t.Errorf("missing %q in:\n%v", want, full)
		}
	}
}
