// Command docsite renders docs/ and the recipes into a static site for
// GitHub Pages. No theme framework: one template, one stylesheet, the
// Markdown as written.
//
//	go run ./tools/docsite -out site
package main

import (
	"bytes"
	"flag"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
)

type page struct {
	Title, Path, Section string
	// NavTitle is the short form for the index down the side. A page heading is
	// written for the page — "Islet — Open-Source Roadmap" — and reads badly in
	// a column of twenty-seven of them.
	NavTitle string
	// Source is the Markdown this page was rendered from, so every page can
	// link to the file somebody would edit to change it.
	Source string
	Body   template.HTML
	Order  int
}

var titleRe = regexp.MustCompile(`(?m)^#\s+(.+)$`)
var mdLinkRe = regexp.MustCompile(`\]\(([^)#:]+)\.md(#[^)]*)?\)`)

// Every Markdown link, not only the ones to other Markdown files: a link to
// docs/openapi.yaml or to a directory is just as broken on a site that does not
// carry those.
var anyLinkRe = regexp.MustCompile(`\]\(([^)\s]+)\)`)

func main() {
	out := flag.String("out", "site", "output directory")
	// Where these pages will live, which the canonical link and the links back
	// to the marketing site need to know. The defaults are what islet.dev
	// serves; a local render can leave them alone and still work, since every
	// link between pages is relative.
	site := flag.String("site", "https://islet.dev", "site origin, for canonical links and links out")
	base := flag.String("base", "/docs/", "path these pages are served under")
	fonts := flag.Bool("fonts", true, "link the site's webfonts at /fonts/fonts.css")
	flag.Parse()
	md := goldmark.New(goldmark.WithExtensions(extension.GFM), goldmark.WithParserOptions(parser.WithAutoHeadingID()), goldmark.WithRendererOptions(html.WithUnsafe()))
	sources := []struct {
		src, dst, section string
		order             int
	}{
		{"README.md", "index.html", "Start", 0},
		// VISION.md is deliberately not published. It is a strategy document —
		// competitor analysis, what might one day be sold and for how much —
		// and a documentation site is read by people deciding whether to trust
		// the software, not by people who want to know what it might cost in
		// two years. It stays in the repository, where anybody who wants the
		// reasoning can read it; it is not part of the manual.
		{"docs/ROADMAP.md", "roadmap.html", "Project", 11},
		{"docs/DECISIONS.md", "decisions.html", "Project", 12},
		{"docs/STRUCTURE.md", "structure.html", "Project", 13},
		{"SECURITY.md", "security.html", "Project", 14},
		{"CONTRIBUTING.md", "contributing.html", "Project", 15},
		{"TRADEMARK.md", "trademark.html", "Project", 16},
		{"catalog/README.md", "catalog.html", "Reference", 20},
		{"examples/README.md", "examples.html", "Reference", 21},
	}
	recipes, _ := filepath.Glob("docs/recipes/*.md")
	sort.Strings(recipes)
	for i, r := range recipes {
		name := strings.TrimSuffix(filepath.Base(r), ".md")
		dst := "recipes/" + name + ".html"
		if name == "README" {
			dst = "recipes/index.html"
		}
		sources = append(sources, struct {
			src, dst, section string
			order             int
		}{r, dst, "Recipes", 100 + i})
	}
	// Which Markdown file becomes which page. A link to something in here
	// becomes a link between pages; a link to anything else — AGENT_SETUP.md,
	// the brand notes, the agent instructions — becomes a link to the file on
	// GitHub. Rewriting every .md to .html regardless is what this used to do,
	// and it produced a documentation site with dead links in its first
	// paragraph.
	rendered := map[string]string{}
	for _, s := range sources {
		rendered[filepath.ToSlash(filepath.Clean(s.src))] = s.dst
	}

	// Images the pages point at, copied in beside them.
	assets := map[string]bool{}

	var pages []page
	for _, s := range sources {
		b, err := os.ReadFile(s.src)
		if err != nil {
			continue
		}
		title := strings.TrimSuffix(filepath.Base(s.src), ".md")
		if m := titleRe.FindSubmatch(b); m != nil {
			title = string(m[1])
		}
		src := rewriteLinks(string(b), s.src, s.dst, rendered, assets)
		var buf bytes.Buffer
		if err := md.Convert([]byte(src), &buf); err != nil {
			fmt.Fprintln(os.Stderr, s.src, err)
			os.Exit(1)
		}
		// A table wider than the column scrolls inside its own box rather than
		// stretching the page sideways — the rule the panel follows, for the
		// same reason: on a phone the alternative is a page that pans.
		body := rewriteHTMLRefs(buf.String(), s.src, s.dst, rendered, assets)
		body = strings.ReplaceAll(body, "<table>", `<div class="tablewrap"><table>`)
		body = strings.ReplaceAll(body, "</table>", "</table></div>")
		pages = append(pages, page{Title: title, NavTitle: navTitle(title), Path: s.dst, Section: s.section, Source: s.src, Body: template.HTML(body), Order: s.order})
	}
	sort.Slice(pages, func(i, j int) bool { return pages[i].Order < pages[j].Order })
	tpl := template.Must(template.New("p").Parse(pageTemplate))
	for _, p := range pages {
		dst := filepath.Join(*out, p.Path)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		f, err := os.Create(dst)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		root := "./"
		if strings.Contains(p.Path, "/") {
			root = "../"
		}
		if err := tpl.Execute(f, map[string]any{
			"Page": p, "Pages": pages, "Root": root,
			"Site": strings.TrimRight(*site, "/"), "Base": *base, "Fonts": *fonts,
		}); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		f.Close()
	}
	for a := range assets {
		dst := filepath.Join(*out, "assets", a)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		b, err := os.ReadFile(a)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if err := os.WriteFile(dst, b, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}

	// Nothing ships with a link to a page that is not there. The website's own
	// build refuses for the same reason, and these pages are about to be a
	// public manual: a dead link in the first paragraph is what this is for.
	if bad := deadLinks(*out); len(bad) > 0 {
		fmt.Fprintln(os.Stderr, "links that resolve to nothing:")
		for _, b := range bad {
			fmt.Fprintln(os.Stderr, "  "+b)
		}
		os.Exit(1)
	}

	_ = os.WriteFile(filepath.Join(*out, ".nojekyll"), nil, 0o644)
	// pages.txt is for whatever publishes these: the website's build reads it
	// to put the documentation in the sitemap, rather than walking the output
	// and guessing which files are pages.
	var list strings.Builder
	for _, p := range pages {
		list.WriteString(p.Path)
		list.WriteByte('\n')
	}
	_ = os.WriteFile(filepath.Join(*out, "pages.txt"), []byte(list.String()), 0o644)
	fmt.Printf("wrote %d pages to %s\n", len(pages), *out)
}

// deadLinks reports every relative link in the rendered site that points at
// nothing. Anchors, external links and the site's own /fonts are somebody
// else's business; what is checked is the links this tool wrote.
func deadLinks(out string) []string {
	var bad []string
	_ = filepath.Walk(out, func(path string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() || !strings.HasSuffix(path, ".html") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		dir := filepath.Dir(path)
		for _, m := range hrefRe.FindAllSubmatch(b, -1) {
			target := string(m[1])
			if i := strings.IndexByte(target, '#'); i >= 0 {
				target = target[:i]
			}
			if target == "" || strings.HasPrefix(target, "#") || strings.HasPrefix(target, "/") ||
				strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
				continue
			}
			if _, err := os.Stat(filepath.Join(dir, target)); err != nil {
				rel, _ := filepath.Rel(out, path)
				bad = append(bad, rel+" → "+target)
			}
		}
		return nil
	})
	sort.Strings(bad)
	return bad
}

var hrefRe = regexp.MustCompile(`(?:href|src)="([^"]+)"`)

// navTitle trims what a heading says twice. "Islet — Product Vision" is a good
// title on its own page and a bad one in a list that already says Islet at the
// top of it.
func navTitle(title string) string {
	for _, p := range []string{"Islet — ", "Islet – ", "Islet: ", "Islet "} {
		if rest := strings.TrimPrefix(title, p); rest != title && rest != "" {
			return strings.ToUpper(rest[:1]) + rest[1:]
		}
	}
	return title
}

// isAsset reports whether a path is something a page displays rather than links
// to, and therefore something this site has to carry itself.
func isAsset(p string) bool {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".svg", ".png", ".jpg", ".jpeg", ".gif", ".webp", ".avif":
		return true
	}
	return false
}

// rewriteLinks turns Markdown links to other files into links a reader can
// follow from this page.
//
// The target is resolved against the directory of the file the link is written
// in, which is how Markdown means it and how GitHub reads it. If that file is
// one of the pages being rendered, the link becomes a relative path to it; if
// it is not, the link goes to the file on GitHub rather than to a page that
// does not exist.
func rewriteLinks(body, srcPath, dstPath string, rendered map[string]string, assets map[string]bool) string {
	return anyLinkRe.ReplaceAllStringFunc(body, func(m string) string {
		g := anyLinkRe.FindStringSubmatch(m)
		to, ok := resolveRef(g[1], srcPath, dstPath, rendered, assets)
		if !ok {
			return m
		}
		return "](" + to + ")"
	})
}

// rewriteHTMLRefs does the same for raw HTML written inside the Markdown, which
// the Markdown pass never sees. The README's logo is an <img src="brand/…">,
// and it was the one dead link the checker found on the first run.
func rewriteHTMLRefs(body, srcPath, dstPath string, rendered map[string]string, assets map[string]bool) string {
	return hrefRe.ReplaceAllStringFunc(body, func(m string) string {
		g := hrefRe.FindStringSubmatch(m)
		to, ok := resolveRef(g[1], srcPath, dstPath, rendered, assets)
		if !ok {
			return m
		}
		attr := "href"
		if strings.HasPrefix(m, "src=") {
			attr = "src"
		}
		return attr + `="` + to + `"`
	})
}

// resolveRef turns one reference written in a source file into one a reader can
// follow from the rendered page, and reports false when it should be left
// alone.
//
// Three outcomes. A file that is also a page here becomes a relative link to
// it. An image becomes a copy served from this site — it has to be, because the
// site's Content-Security-Policy allows no third-party origins, and a project
// arguing "own your infrastructure" should not ask GitHub for its own wordmark.
// Anything else — AGENT_SETUP.md, an OpenAPI file, a directory — becomes a link
// to the repository, which is where it actually is.
func resolveRef(ref, srcPath, dstPath string, rendered map[string]string, assets map[string]bool) (string, bool) {
	target, frag := ref, ""
	if i := strings.IndexByte(target, '#'); i >= 0 {
		target, frag = target[:i], target[i:]
	}
	switch {
	case target == "", strings.HasPrefix(target, "#"), strings.HasPrefix(target, "/"),
		strings.Contains(target, "://"), strings.HasPrefix(target, "mailto:"):
		return "", false
	}
	srcDir := filepath.ToSlash(filepath.Dir(srcPath))
	dstDir := filepath.ToSlash(filepath.Dir(dstPath))
	if dstDir == "." {
		dstDir = ""
	}
	repoPath := filepath.ToSlash(filepath.Clean(filepath.Join(srcDir, target)))
	rel := func(to string) string {
		r, err := filepath.Rel(dstDir, to)
		if err != nil {
			return to
		}
		return filepath.ToSlash(r)
	}
	if out, ok := rendered[repoPath]; ok {
		return rel(filepath.ToSlash(out)) + frag, true
	}
	if isAsset(repoPath) {
		if fi, err := os.Stat(repoPath); err == nil && !fi.IsDir() {
			assets[repoPath] = true
			return rel("assets/"+repoPath) + frag, true
		}
	}
	if fi, err := os.Stat(repoPath); err == nil && fi.IsDir() {
		return repoTree + repoPath + frag, true
	}
	return repoBlob + repoPath + frag, true
}

// Where a file that is not part of the documentation site can be read instead.
const (
	repoBlob = "https://github.com/isletdev/islet/blob/main/"
	repoTree = "https://github.com/isletdev/islet/tree/main/"
)

const pageTemplate = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{if eq .Page.Path "index.html"}}Islet documentation{{else}}{{.Page.Title}} · Islet docs{{end}}</title>
<meta name="description" content="{{.Page.Title}} — documentation for Islet, a control panel for one server.">
<link rel="canonical" href="{{.Site}}{{.Base}}{{.Page.Path}}">
<meta name="theme-color" content="#FFFFFF" media="(prefers-color-scheme: light)">
<meta name="theme-color" content="#0A0A0A" media="(prefers-color-scheme: dark)">
{{if .Fonts}}<link rel="stylesheet" href="/fonts/fonts.css">{{end}}
<style>
/* The same palette and type as islet.dev, so the documentation reads as part of
   the site rather than as something bolted to the side of it. The font files
   are the site's own: without them the stack falls back to the system face and
   nothing else changes. */
:root {
  color-scheme: light dark;
  --bg: #FFFFFF; --surface: #FAFAFA; --border: #E5E5E5; --border-strong: #8A8A8A;
  --ink: #0A0A0A; --muted: #6B6B6B; --faint: #A3A3A3; --accent: #0A5BFF;
  --code-bg: #F4F4F5; --code-fg: #0A0A0A;
  --sans: "Geist", system-ui, -apple-system, "Segoe UI", Roboto, sans-serif;
  --mono: "Geist Mono", ui-monospace, "SFMono-Regular", Menlo, Consolas, monospace;
}
@media (prefers-color-scheme: dark) {
  :root {
    --bg: #0A0A0A; --surface: #111111; --border: #262626; --border-strong: #4D4D4D;
    --ink: #FAFAFA; --muted: #A3A3A3; --faint: #6B6B6B; --accent: #5B9BFF;
    --code-bg: #111111; --code-fg: #FAFAFA;
  }
}
* { box-sizing: border-box; }
body { margin: 0; font-family: var(--sans); font-size: 16px; line-height: 1.65; color: var(--ink); background: var(--bg); -webkit-font-smoothing: antialiased; }
a { color: var(--accent); }

header.top { position: sticky; top: 0; z-index: 10; display: flex; align-items: center; gap: 16px; padding: 0 20px; height: 56px; border-bottom: 1px solid var(--border); background: color-mix(in srgb, var(--bg) 88%, transparent); backdrop-filter: blur(8px); }
header.top .brand { font-weight: 600; letter-spacing: -0.02em; color: var(--ink); text-decoration: none; font-size: 1rem; }
header.top .brand span { color: var(--muted); font-weight: 400; }
header.top nav { margin-left: auto; display: flex; gap: 18px; font-size: 0.9375rem; }
header.top nav a { color: var(--muted); text-decoration: none; }
header.top nav a:hover { color: var(--ink); }
header.top .menu { display: none; }

/* The index is written after the page in the document and put back on the left
   by the grid. On a phone, where there is one column, that leaves the page you
   opened at the top instead of twenty-seven links you did not ask for. */
.wrap { display: grid; grid-template-columns: 240px minmax(0, 1fr); align-items: start; }
aside { grid-column: 1; grid-row: 1; position: sticky; top: 56px; max-height: calc(100dvh - 56px); overflow-y: auto; border-right: 1px solid var(--border); padding: 24px 16px 40px; font-size: 0.875rem; }
main { grid-column: 2; grid-row: 1; }
aside .sec { margin: 18px 0 6px; text-transform: uppercase; font-size: 0.6875rem; letter-spacing: .06em; color: var(--faint); }
aside .sec:first-child { margin-top: 0; }
aside a { display: block; color: var(--muted); text-decoration: none; padding: 4px 8px; margin: 0 -8px; border-radius: 6px; }
aside a:hover { color: var(--ink); background: var(--surface); }
aside a.on { color: var(--ink); background: var(--surface); font-weight: 500; }

main { padding: 40px 40px 96px; max-width: 76ch; min-width: 0; }
main h1 { font-size: 2rem; letter-spacing: -0.03em; margin: 0 0 8px; }
main h2 { font-size: 1.25rem; letter-spacing: -0.02em; margin: 40px 0 8px; padding-top: 8px; border-top: 1px solid var(--border); }
main h3 { font-size: 1rem; margin: 28px 0 4px; }
main p, main li { color: var(--ink); }
main blockquote { border-left: 2px solid var(--border-strong); margin: 16px 0; padding: 0 0 0 14px; color: var(--muted); }
main img { max-width: 100%; }
hr { border: 0; border-top: 1px solid var(--border); margin: 32px 0; }

pre { background: var(--code-bg); color: var(--code-fg); padding: 14px 16px; border-radius: 8px; border: 1px solid var(--border); overflow-x: auto; font-size: 0.8125rem; line-height: 1.55; }
code, kbd, pre { font-family: var(--mono); }
code { background: var(--code-bg); padding: .12em .35em; border-radius: 4px; font-size: 0.875em; }
pre code { padding: 0; background: none; font-size: inherit; }

.tablewrap { overflow-x: auto; }
table { border-collapse: collapse; width: 100%; font-size: 0.9375rem; }
th, td { border-bottom: 1px solid var(--border); text-align: left; padding: 8px 10px; vertical-align: top; }
th { font-weight: 600; }

footer { border-top: 1px solid var(--border); margin-top: 56px; padding-top: 20px; font-size: 0.875rem; color: var(--muted); }

@media (max-width: 860px) {
  .wrap { grid-template-columns: 1fr; }
  aside { grid-column: 1; grid-row: 2; position: static; max-height: none; border-right: 0; border-top: 1px solid var(--border); padding: 20px; }
  main { grid-column: 1; grid-row: 1; padding: 24px 20px 40px; }
  header.top nav a.hide-sm { display: none; }
}
</style>
</head>
<body>
<header class="top">
  <a class="brand" href="{{.Site}}/">Islet <span>docs</span></a>
  <nav>
    <a class="hide-sm" href="{{.Site}}/#features">Features</a>
    <a class="hide-sm" href="{{.Site}}/#install">Install</a>
    <a href="https://github.com/isletdev/islet">GitHub</a>
  </nav>
</header>
<div class="wrap">
<main>
{{.Page.Body}}
<footer>
  Islet runs one server, and this is its documentation. Found something wrong?
  <a href="https://github.com/isletdev/islet/blob/main/{{.Page.Source}}">Edit this page</a>.
</footer>
</main>
<aside>
{{$root := .Root}}{{$cur := .Page.Path}}{{$sec := ""}}
{{range .Pages}}{{if ne .Section $sec}}{{$sec = .Section}}<div class="sec">{{.Section}}</div>{{end}}<a href="{{$root}}{{.Path}}"{{if eq .Path $cur}} class="on"{{end}}>{{.NavTitle}}</a>
{{end}}
</aside>
</div>
</body>
</html>
`
