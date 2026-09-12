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
	Body                 template.HTML
	Order                int
}

var titleRe = regexp.MustCompile(`(?m)^#\s+(.+)$`)
var mdLinkRe = regexp.MustCompile(`\]\(([^)#:]+)\.md(#[^)]*)?\)`)

func main() {
	out := flag.String("out", "site", "output directory")
	flag.Parse()
	md := goldmark.New(goldmark.WithExtensions(extension.GFM), goldmark.WithParserOptions(parser.WithAutoHeadingID()), goldmark.WithRendererOptions(html.WithUnsafe()))
	sources := []struct {
		src, dst, section string
		order             int
	}{
		{"README.md", "index.html", "Start", 0},
		{"docs/VISION.md", "vision.html", "Project", 10},
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
		// Links between Markdown files become links between pages.
		src := mdLinkRe.ReplaceAllString(string(b), "]($1.html$2)")
		src = strings.ReplaceAll(src, "](docs/recipes/", "](recipes/")
		src = strings.ReplaceAll(src, "](docs/", "](")
		var buf bytes.Buffer
		if err := md.Convert([]byte(src), &buf); err != nil {
			fmt.Fprintln(os.Stderr, s.src, err)
			os.Exit(1)
		}
		pages = append(pages, page{Title: title, Path: s.dst, Section: s.section, Body: template.HTML(buf.String()), Order: s.order})
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
		if err := tpl.Execute(f, map[string]any{"Page": p, "Pages": pages, "Root": root}); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		f.Close()
	}
	_ = os.WriteFile(filepath.Join(*out, ".nojekyll"), nil, 0o644)
	fmt.Printf("wrote %d pages to %s\n", len(pages), *out)
}

const pageTemplate = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Page.Title}} · Islet</title>
<style>
:root { color-scheme: light dark; --ink: #111; --muted: #666; --line: #e5e5e5; --bg: #fff; --accent: #2563eb; --code: #f4f4f5; }
@media (prefers-color-scheme: dark) { :root { --ink: #ededed; --muted: #9a9a9a; --line: #262626; --bg: #0a0a0a; --accent: #60a5fa; --code: #161616; } }
* { box-sizing: border-box; }
body { margin: 0; font: 15px/1.6 system-ui, -apple-system, "Segoe UI", sans-serif; color: var(--ink); background: var(--bg); }
.wrap { display: grid; grid-template-columns: 220px minmax(0, 1fr); min-height: 100vh; }
nav { border-right: 1px solid var(--line); padding: 20px 16px; font-size: 13px; }
nav h1 { font-size: 15px; margin: 0 0 12px; letter-spacing: -0.01em; }
nav .sec { margin: 14px 0 4px; text-transform: uppercase; font-size: 11px; letter-spacing: .05em; color: var(--muted); }
nav a { display: block; color: var(--ink); text-decoration: none; padding: 3px 0; }
nav a.on { color: var(--accent); font-weight: 600; }
main { padding: 32px 40px 80px; max-width: 78ch; }
main h1 { letter-spacing: -0.02em; }
main a { color: var(--accent); }
pre { background: var(--code); padding: 12px; border-radius: 6px; overflow-x: auto; font-size: 13px; }
code { background: var(--code); padding: .1em .3em; border-radius: 3px; font-size: 13px; }
pre code { padding: 0; background: none; }
table { border-collapse: collapse; width: 100%; font-size: 14px; }
th, td { border-bottom: 1px solid var(--line); text-align: left; padding: 6px 8px; vertical-align: top; }
blockquote { border-left: 3px solid var(--line); margin: 0; padding: 0 0 0 12px; color: var(--muted); }
@media (max-width: 720px) { .wrap { grid-template-columns: 1fr; } nav { border-right: 0; border-bottom: 1px solid var(--line); } main { padding: 20px; } }
</style>
</head>
<body>
<div class="wrap">
<nav>
<h1><a href="{{.Root}}index.html">Islet</a></h1>
{{$root := .Root}}{{$cur := .Page.Path}}{{$sec := ""}}
{{range .Pages}}{{if ne .Section $sec}}{{$sec = .Section}}<div class="sec">{{.Section}}</div>{{end}}<a href="{{$root}}{{.Path}}"{{if eq .Path $cur}} class="on"{{end}}>{{.Title}}</a>
{{end}}
<div class="sec">Code</div><a href="https://github.com/isletdev/islet">GitHub</a>
</nav>
<main>
{{.Page.Body}}
</main>
</div>
</body>
</html>
`
