//go:build !windows

package files

import (
	"os"
	"os/user"
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

// Chown changes owner and group by name or numeric id.
func Chown(p, owner, group string) error {
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
	return os.Lchown(p, uid, gid)
}
