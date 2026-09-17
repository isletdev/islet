package github

import (
	"slices"
	"testing"
)

// The point of this list is to turn "Resource not accessible by integration"
// into a sentence naming the box to tick. It is worth nothing if it cries wolf:
// a permission already granted must never be reported missing.
func TestMissingPermissions(t *testing.T) {
	full := &AppInfo{
		Name: "Islet",
		Permissions: map[string]string{
			"metadata": "read", "contents": "read", "administration": "write",
			"organization_self_hosted_runners": "write",
		},
		Events: []string{"push", "workflow_job"},
	}
	if got := Missing(full); len(got) != 0 {
		t.Fatalf("a correctly configured App was told to fix %v", names(got))
	}

	// GitHub is not consistent about the organisation prefix — "members" has
	// none, "organization_self_hosted_runners" does — so either spelling counts.
	short := &AppInfo{
		Permissions: map[string]string{
			"metadata": "read", "contents": "read", "administration": "write",
			"self_hosted_runners": "write",
		},
		Events: []string{"push", "workflow_job"},
	}
	if got := Missing(short); len(got) != 0 {
		t.Fatalf("the unprefixed spelling was not recognised: %v", names(got))
	}

	// More than was asked for is not less.
	generous := &AppInfo{
		Permissions: map[string]string{
			"metadata": "write", "contents": "write", "administration": "write",
			"organization_self_hosted_runners": "write",
		},
		Events: []string{"push", "workflow_job", "issues"},
	}
	if got := Missing(generous); len(got) != 0 {
		t.Fatalf("write was not accepted where read was asked: %v", names(got))
	}

	// The state this server was actually in: installed, able to authenticate,
	// and refused by every org runner call with a 403.
	real := &AppInfo{
		Permissions: map[string]string{"metadata": "read", "contents": "read"},
		Events:      []string{"push", "workflow_job"},
	}
	got := names(Missing(real))
	want := []string{"administration", "organization_self_hosted_runners"}
	if !slices.Equal(got, want) {
		t.Fatalf("missing = %v, want %v", got, want)
	}

	// An event that was never subscribed to is as blocking as a permission,
	// and reads the same way in the panel.
	noEvents := &AppInfo{
		Permissions: map[string]string{
			"metadata": "read", "contents": "read", "administration": "write",
			"organization_self_hosted_runners": "write",
		},
	}
	if got := names(Missing(noEvents)); !slices.Equal(got, []string{"push", "workflow_job"}) {
		t.Fatalf("missing events = %v", got)
	}

	if Missing(nil) != nil {
		t.Error("an App that could not be read should report nothing, not everything")
	}
}

func names(ns []Need) []string {
	out := []string{}
	for _, n := range ns {
		out = append(out, n.Name)
	}
	return out
}
