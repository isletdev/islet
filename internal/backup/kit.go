package backup

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"time"
)

// A recovery kit is not a file this server keeps. It is written from the
// destinations' current keys each time it is downloaded, so a new download is
// as valid as the first one, and an old one stays valid for as long as the
// repository it names still accepts the key in it. What can make an old kit
// useless is a destination removed and added again — that generates a new key
// for a new repository — or a repository that has since been deleted.
//
// CheckKit answers "does this kit still open my backups" the only honest way:
// it opens each repository with the kit's own credentials and lists what is
// there. Nothing from the kit is stored.

// KitCheck is what opening one of a kit's repositories found.
type KitCheck struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	Repo      string `json:"repo"`
	OK        bool   `json:"ok"`
	Snapshots int    `json:"snapshots"`
	Latest    string `json:"latest,omitempty"`
	Error     string `json:"error,omitempty"`
	// Here is how the repository relates to this server's destinations:
	// "same" when one points at it with the same key, "other-key" when one
	// points at it with a different key, "" when none does.
	Here     string `json:"here"`
	HereName string `json:"hereName,omitempty"`
}

// KitReport is the whole check.
type KitReport struct {
	GeneratedAt  string     `json:"generatedAt"`
	Hostname     string     `json:"hostname"`
	Destinations []KitCheck `json:"destinations"`
	// Uncovered names this server's destinations the kit says nothing about:
	// backups that kit could not restore.
	Uncovered []string `json:"uncovered"`
}

// ErrNotAKit is a file that is not a recovery kit.
var ErrNotAKit = errors.New("this is not an Islet recovery kit")

// CheckKit opens every repository a kit names with the kit's credentials.
func (s *Service) CheckKit(ctx context.Context, actor string, raw []byte) (*KitReport, error) {
	var kit struct {
		Islet        string        `json:"islet"`
		GeneratedAt  string        `json:"generatedAt"`
		Hostname     string        `json:"hostname"`
		Destinations []Destination `json:"destinations"`
	}
	if err := json.Unmarshal(raw, &kit); err != nil || kit.Islet != "recovery kit" {
		return nil, ErrNotAKit
	}
	if len(kit.Destinations) == 0 {
		return nil, errors.New("the kit names no backup destinations")
	}
	current, err := s.Destinations(ctx)
	if err != nil {
		return nil, err
	}
	rep := &KitReport{GeneratedAt: kit.GeneratedAt, Hostname: kit.Hostname, Destinations: []KitCheck{}, Uncovered: []string{}}
	covered := map[string]bool{}
	for _, d := range kit.Destinations {
		if d.Config == nil {
			d.Config = map[string]string{}
		}
		c := KitCheck{Name: d.Name, Type: d.Type}
		if err := d.Validate(); err != nil {
			c.Error = "incomplete in the kit: " + err.Error()
			rep.Destinations = append(rep.Destinations, c)
			continue
		}
		c.Repo = d.display()
		covered[c.Repo] = true
		for _, cur := range current {
			if cur.display() == c.Repo {
				c.HereName, c.Here = cur.Name, "other-key"
				if cur.Password == d.Password {
					c.Here = "same"
				}
			}
		}
		// A local repository is a folder. resticArgs creates one that is
		// missing, which is right for a backup and wrong for a check.
		if d.Type == "local" {
			if st, err := os.Stat(d.Config["path"]); err != nil || !st.IsDir() {
				c.Error = "the folder " + d.Config["path"] + " is not on this server"
				rep.Destinations = append(rep.Destinations, c)
				continue
			}
		}
		if d.Password == "" {
			c.Error = "the kit holds no key for this repository"
			rep.Destinations = append(rep.Destinations, c)
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		snaps, err := s.snapshotsOf(cctx, actor, &d, "snapshots", "--json", "--no-lock")
		cancel()
		if err != nil {
			c.Error = err.Error()
		} else {
			c.OK, c.Snapshots = true, len(snaps)
			if len(snaps) > 0 {
				c.Latest = snaps[0].Time
			}
		}
		rep.Destinations = append(rep.Destinations, c)
	}
	for _, cur := range current {
		if !covered[cur.display()] {
			rep.Uncovered = append(rep.Uncovered, cur.Name)
		}
	}
	_ = s.st.Audit(ctx, actor, "backup.kit.check", "", kit.GeneratedAt)
	return rep, nil
}
