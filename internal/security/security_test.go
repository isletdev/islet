package security

import (
	"strings"
	"testing"
)

func TestParseSSHD(t *testing.T) {
	cfg := ParseSSHD(`
# comment
Port 2222
PermitRootLogin prohibit-password
PasswordAuthentication no
PasswordAuthentication yes
MaxAuthTries 3
Match User deploy
    PasswordAuthentication yes
`)
	if cfg.Port != 2222 || cfg.PermitRoot || cfg.PasswordAuth || cfg.MaxAuthTries != 3 {
		t.Fatalf("parsed %+v", cfg)
	}
	def := ParseSSHD("")
	if def.Port != 22 || !def.PermitRoot || !def.PasswordAuth {
		t.Fatalf("defaults %+v", def)
	}
}

func TestRenderAndValidate(t *testing.T) {
	cfg := SSHSettings{Port: 22, PermitRoot: false, PasswordAuth: false, PubkeyAuth: true, MaxAuthTries: 4, ClientAliveMax: 3}
	out := RenderSSHD(cfg)
	for _, want := range []string{"PermitRootLogin no", "PasswordAuthentication no", "PubkeyAuthentication yes", "MaxAuthTries 4"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if err := cfg.Validate(false); err == nil {
		t.Error("turning off passwords without keys must be refused")
	}
	if err := cfg.Validate(true); err != nil {
		t.Errorf("valid config rejected: %v", err)
	}
	bad := cfg
	bad.PubkeyAuth = false
	if err := bad.Validate(true); err == nil {
		t.Error("no auth method at all must be refused")
	}
	// Round trip: what we render, we read back.
	if got := ParseSSHD(out); got.Port != 22 || got.PermitRoot || got.PasswordAuth || !got.PubkeyAuth || got.MaxAuthTries != 4 {
		t.Errorf("round trip %+v", got)
	}
}
