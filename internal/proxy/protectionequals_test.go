package proxy

import "testing"

// The API holds a change to who may reach a site to a higher bar than a change
// to where it points, so this has to say "no change" for a save that only looks
// different and "change" for anything that moves a person or a path.
func TestProtectionEquals(t *testing.T) {
	base := func() *Domain {
		return &Domain{
			Host: "a.example.com", Protect: true, ProtectUsers: "alice,bob",
			Locations: []Location{
				{Path: "/admin", Protect: "on", ProtectUsers: "alice"},
				{Path: "/hooks", Protect: "off"},
				{Path: "/api"},
			},
		}
	}
	same := []struct {
		name string
		edit func(*Domain)
	}{
		{"nothing at all", func(*Domain) {}},
		{"the list retyped", func(d *Domain) { d.ProtectUsers = " Alice , bob " }},
		{"a location's list retyped", func(d *Domain) { d.Locations[0].ProtectUsers = "ALICE" }},
		{"an empty protect means inherit", func(d *Domain) { d.Locations[2].Protect = "" }},
		{"a path with a trailing slash", func(d *Domain) { d.Locations[1].Path = "/hooks/" }},
		{"where it points", func(d *Domain) { d.Target, d.Port = "other", 9000 }},
		{"a location's backend", func(d *Domain) { d.Locations[0].Target = "elsewhere" }},
	}
	for _, c := range same {
		d := base()
		c.edit(d)
		if !d.ProtectionEquals(base()) {
			t.Errorf("%s: reported as a protection change", c.name)
		}
	}
	changed := []struct {
		name string
		edit func(*Domain)
	}{
		{"the gate turned off", func(d *Domain) { d.Protect = false }},
		{"a name added", func(d *Domain) { d.ProtectUsers = "alice,bob,carol" }},
		{"a name removed", func(d *Domain) { d.ProtectUsers = "alice" }},
		{"the list emptied, which lets in everyone", func(d *Domain) { d.ProtectUsers = "" }},
		{"a path opened", func(d *Domain) { d.Locations[2].Protect = "off" }},
		{"a path's list widened", func(d *Domain) { d.Locations[0].ProtectUsers = "alice,bob" }},
		{"a new open path", func(d *Domain) { d.Locations = append(d.Locations, Location{Path: "/new", Protect: "off"}) }},
		{"a protected path removed", func(d *Domain) { d.Locations = d.Locations[1:] }},
	}
	for _, c := range changed {
		d := base()
		c.edit(d)
		if d.ProtectionEquals(base()) {
			t.Errorf("%s: went unnoticed", c.name)
		}
	}
	// A new domain is compared against nothing.
	if (&Domain{}).ProtectionEquals(nil) == false {
		t.Error("an unprotected new domain counts as a protection change")
	}
	if (&Domain{Protect: true}).ProtectionEquals(nil) {
		t.Error("a new domain created with a gate is not a protection change")
	}
}
