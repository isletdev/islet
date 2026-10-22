//go:build !windows

package files

import (
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
)

var (
	nameMu sync.Mutex
	uids   = map[uint32]string{}
	gids   = map[uint32]string{}
)

func ownerOf(info os.FileInfo) (string, string) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", ""
	}
	nameMu.Lock()
	defer nameMu.Unlock()
	u, ok := uids[st.Uid]
	if !ok {
		u = strconv.Itoa(int(st.Uid))
		if usr, err := user.LookupId(u); err == nil {
			u = usr.Username
		}
		uids[st.Uid] = u
	}
	g, ok := gids[st.Gid]
	if !ok {
		g = strconv.Itoa(int(st.Gid))
		if grp, err := user.LookupGroupId(g); err == nil {
			g = grp.Name
		}
		gids[st.Gid] = g
	}
	return u, g
}

// sameOwner gives p the owner and group of like. The daemon runs as root, so
// anything it creates is root's unless it says otherwise: a folder made in
// somebody's project came out root:root and they could not open it. Copying
// the owner of the folder it was made in is what a person creating it there
// would have got. Not being root is not an error — then everything is already
// the caller's own.
func sameOwner(p string, like os.FileInfo) {
	if st, ok := like.Sys().(*syscall.Stat_t); ok && os.Geteuid() == 0 {
		_ = os.Lchown(p, int(st.Uid), int(st.Gid))
	}
}

// Chown changes owner and group by name or numeric id, and with recursive
// everything beneath a directory as well. Links are changed themselves and
// never followed, so a link inside a project cannot hand over its target.
func Chown(p, owner, group string, recursive bool) error {
	p, err := Clean(p)
	if err != nil {
		return err
	}
	uid, gid := -1, -1
	if owner != "" {
		if u, err := user.Lookup(owner); err == nil {
			uid, _ = strconv.Atoi(u.Uid)
		} else if n, err := strconv.Atoi(owner); err == nil {
			uid = n
		} else {
			return err
		}
	}
	if group != "" {
		if g, err := user.LookupGroup(group); err == nil {
			gid, _ = strconv.Atoi(g.Gid)
		} else if n, err := strconv.Atoi(group); err == nil {
			gid = n
		} else {
			return err
		}
	}
	if !recursive {
		return os.Lchown(p, uid, gid)
	}
	return filepath.WalkDir(p, func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Lchown(path, uid, gid)
	})
}
