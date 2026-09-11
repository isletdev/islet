package backup

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/isletdev/islet/internal/cmdrun"
)

// hook runs a plan's shell command on the host and records it in the
// command log. Output is returned for the run log.
func (s *Service) hook(ctx context.Context, cmd string, env ...string) (string, error) {
	c := exec.CommandContext(ctx, "sh", "-c", cmd)
	c.Env = append(os.Environ(), env...)
	start := time.Now()
	out, err := c.CombinedOutput()
	s.run.Record(ctx, "backup", "sh -c "+cmd, cmdrun.Result{ExitCode: exitCode(err), Duration: time.Since(start), Stdout: string(out)})
	return string(out), err
}

// pause stops every running container that mounts one of the plan's
// volumes and returns their ids so they can be started again.
func (s *Service) pause(ctx context.Context, p *Plan, say func(string)) []string {
	var stopped []string
	for _, src := range p.Sources {
		if src.Type != "volume" {
			continue
		}
		out, err := s.run.Run(ctx, "backup", "docker", "ps", "--filter", "volume="+src.Value, "--format", "{{.Names}}")
		if err != nil {
			continue
		}
		for _, c := range strings.Fields(out.Stdout) {
			if _, err := s.run.Run(ctx, "backup", "docker", "stop", "-t", "30", c); err == nil {
				stopped = append(stopped, c)
				say("[islet] paused " + c + " (uses volume " + src.Value + ")")
			} else {
				say("[islet] could not pause " + c + ": " + err.Error())
			}
		}
	}
	return stopped
}

// DataDir is where restores and dumps live (for callers that move files).
func (s *Service) DataDir() string { return s.dataDir }
