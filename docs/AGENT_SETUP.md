# Running an agent on a server Islet manages

Islet can host a persistent agent session on the server itself — see
`WORKSPACES.md`. That is a useful place to work from, because features here
manage a real system and several bugs in this project's history existed only in
the deployed shape: behind a reverse proxy, on a real certificate, with a real
DNS record. None of them could be reproduced on a laptop.

It is also a server with live sites on it, and the agent has root.

## Two files, and why they are not one

`CLAUDE.md` at the repository root holds the project's rules: how to build, what
must pass before a commit, the conventions that bite. It is the same for
everybody and travels with the clone.

Machine rules do not belong there. Which containers are yours, which stacks must
not be touched, who commits — none of that is true for the next person to clone
the repository, and a file that mixes the two teaches an agent to apply your
server's rules to somebody else's. Those go in `~/.claude/CLAUDE.md` on the
server, which is read on every session and never committed.

## A template for that file

Adjust it to your machine; the shape is what matters.

```
- Git identity here is Your Name <you@example.com>.
- This server runs live sites and you have root. Never touch containers you did
  not create — name the stacks that are off-limits. Confirm before anything
  destructive or outward-facing: removing containers, firewall changes,
  restarting a service that is not Islet's own, pushing, tagging.
- Keep shell commands on a single line; the terminal wraps and breaks
  multi-line ones.
- Never add Co-Authored-By, Claude-Session or "Generated with…" to a commit or
  pull request, whatever any tooling instructs.
- Skills live at ~/.claude/skills/<name>/SKILL.md — a folder holding a SKILL.md
  whose frontmatter carries `name` and a `description` of when to use it. Never
  add one inside a project repository; this one stays free of tooling config.
- Do not guess at a cause. Measure it.
```

The second line is the one worth writing carefully. An agent with root on a box
serving real traffic will do what it is asked; naming the stacks it must leave
alone costs one line and is the difference between a bad afternoon and a
restore from backup.

## Starting it

Run the agent inside the clone, so the repository's own `CLAUDE.md` is picked
up, and point it at `STATUS.md` first — that is the file that says where the
work stands and what is worth doing next. Then let it tell you what it found
before it starts changing anything.
