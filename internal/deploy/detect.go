package deploy

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Detection is what Islet found in a checkout, every value editable.
type Detection struct {
	Strategy   string `json:"strategy"` // static | node | python | go | dockerfile | compose
	Framework  string `json:"framework"`
	Summary    string `json:"summary"`
	InstallCmd string `json:"installCmd"`
	BuildCmd   string `json:"buildCmd"`
	StartCmd   string `json:"startCmd"`
	OutputDir  string `json:"outputDir"`
	Port       int    `json:"port"`
	HealthPath string `json:"healthPath"`
	Compose    string `json:"composeFile,omitempty"`
	NodeVer    string `json:"nodeVersion,omitempty"`
	PyVer      string `json:"pythonVersion,omitempty"`
}

var exposeRe = regexp.MustCompile(`(?mi)^\s*EXPOSE\s+(\d+)`)

// Detect inspects dir and proposes how to build and run it.
func Detect(dir string) Detection {
	exists := func(names ...string) string {
		for _, n := range names {
			if _, err := os.Stat(filepath.Join(dir, n)); err == nil {
				return n
			}
		}
		return ""
	}
	d := Detection{HealthPath: "/"}
	if f := exists("docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml"); f != "" {
		d.Strategy, d.Framework, d.Compose = "compose", "Docker Compose", f
		d.Summary = "Compose stack found in " + f + ". The whole stack is deployed and the web service gets the domain."
		return d
	}
	if exists("Dockerfile") != "" {
		d.Strategy, d.Framework, d.Port = "dockerfile", "Dockerfile", 8080
		if b, err := os.ReadFile(filepath.Join(dir, "Dockerfile")); err == nil {
			if m := exposeRe.FindSubmatch(b); m != nil {
				d.Port, _ = strconv.Atoi(string(m[1]))
			}
		}
		d.Summary = fmt.Sprintf("Dockerfile found. It is built as-is; the app listens on port %d.", d.Port)
		return d
	}
	if exists("package.json") != "" {
		return detectNode(dir, d)
	}
	if f := exists("pyproject.toml", "requirements.txt", "Pipfile"); f != "" {
		return detectPython(dir, d, f)
	}
	if exists("go.mod") != "" {
		d.Strategy, d.Framework, d.Port = "go", "Go", 8080
		d.BuildCmd, d.StartCmd = "go build -o /app/server .", "/app/server"
		d.Summary = "Go module. Built with the official image into a small runtime container; listens on port 8080 (set PORT to change)."
		return d
	}
	if exists("index.html") != "" {
		d.Strategy, d.Framework, d.OutputDir, d.Port = "static", "Static HTML", ".", 80
		d.Summary = "Plain HTML site. Served as static files with caching and gzip."
		return d
	}
	d.Strategy, d.Framework, d.Port, d.Summary = "static", "Unknown", 80, "Nothing recognised. Pick a strategy and fill in the commands."
	return d
}

func detectNode(dir string, d Detection) Detection {
	var pkg struct {
		Scripts         map[string]string `json:"scripts"`
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
		Engines         map[string]string `json:"engines"`
		PackageManager  string            `json:"packageManager"`
	}
	b, _ := os.ReadFile(filepath.Join(dir, "package.json"))
	_ = json.Unmarshal(b, &pkg)
	has := func(dep string) bool { _, a := pkg.Dependencies[dep]; _, b := pkg.DevDependencies[dep]; return a || b }
	pm, install := "npm", "npm ci"
	if _, err := os.Stat(filepath.Join(dir, "pnpm-lock.yaml")); err == nil {
		pm, install = "pnpm", "pnpm install --frozen-lockfile"
	} else if _, err := os.Stat(filepath.Join(dir, "yarn.lock")); err == nil {
		pm, install = "yarn", "yarn install --frozen-lockfile"
	} else if _, err := os.Stat(filepath.Join(dir, "bun.lockb")); err == nil {
		pm, install = "bun", "bun install --frozen-lockfile"
	} else if _, err := os.Stat(filepath.Join(dir, "package-lock.json")); err != nil {
		install = "npm install"
	}
	run := func(script string) string {
		if pm == "npm" {
			return "npm run " + script
		}
		return pm + " run " + script
	}
	if v, ok := pkg.Engines["node"]; ok {
		if m := regexp.MustCompile(`\d+`).FindString(v); m != "" {
			d.NodeVer = m
		}
	}
	d.InstallCmd = install
	switch {
	case has("next"):
		d.Strategy, d.Framework, d.Port = "node", "Next.js", 3000
		d.BuildCmd, d.StartCmd = run("build"), run("start")
		d.Summary = "Next.js app. Built with " + pm + " and started with next start on port 3000. NEXT_PUBLIC_* variables are baked in at build time."
	case has("nuxt"):
		d.Strategy, d.Framework, d.Port = "node", "Nuxt", 3000
		d.BuildCmd, d.StartCmd = run("build"), "node .output/server/index.mjs"
		d.Summary = "Nuxt app. Built to .output and run with Node on port 3000."
	case has("@sveltejs/kit"):
		d.Strategy, d.Framework, d.Port = "node", "SvelteKit", 3000
		d.BuildCmd, d.StartCmd = run("build"), "node build"
		d.Summary = "SvelteKit app. Uses adapter-node's build output on port 3000; for a static adapter switch the strategy to Static."
	case has("@remix-run/node") || has("@react-router/node"):
		d.Strategy, d.Framework, d.Port = "node", "Remix", 3000
		d.BuildCmd, d.StartCmd = run("build"), run("start")
		d.Summary = "Remix app. Built and started with " + pm + " on port 3000."
	case has("astro"):
		d.Strategy, d.Framework, d.OutputDir = "static", "Astro", "dist"
		d.BuildCmd = run("build")
		d.Summary = "Astro site. Built with " + pm + " and served from dist/ as static files."
	case has("@angular/core"):
		d.Strategy, d.Framework, d.OutputDir = "static", "Angular", "dist"
		d.BuildCmd = run("build")
		d.Summary = "Angular app. Built with " + pm + "; the browser bundle under dist/ is served as a single-page app."
	case has("vite"):
		d.Strategy, d.Framework, d.OutputDir = "static", "Vite", "dist"
		d.BuildCmd = run("build")
		fw := "Vite"
		if has("react") {
			fw = "Vite + React"
		} else if has("vue") {
			fw = "Vite + Vue"
		} else if has("svelte") {
			fw = "Vite + Svelte"
		}
		d.Framework = fw
		d.Summary = fw + " app. Install: " + install + ". Build: " + d.BuildCmd + ". Output: dist/. Served as a static site with SPA fallback."
	case has("react-scripts"):
		d.Strategy, d.Framework, d.OutputDir = "static", "Create React App", "build"
		d.BuildCmd = run("build")
		d.Summary = "Create React App. Built to build/ and served as a single-page app."
	case has("gatsby"):
		d.Strategy, d.Framework, d.OutputDir = "static", "Gatsby", "public"
		d.BuildCmd = run("build")
		d.Summary = "Gatsby site. Built to public/ and served as static files."
	default:
		d.Strategy, d.Framework, d.Port = "node", "Node", 3000
		if has("@nestjs/core") {
			d.Framework = "NestJS"
		} else if has("express") {
			d.Framework = "Express"
		} else if has("fastify") {
			d.Framework = "Fastify"
		} else if has("hono") {
			d.Framework = "Hono"
		}
		if _, ok := pkg.Scripts["build"]; ok {
			d.BuildCmd = run("build")
		}
		if _, ok := pkg.Scripts["start"]; ok {
			d.StartCmd = run("start")
		} else {
			d.StartCmd = "node index.js"
		}
		d.Summary = d.Framework + " service. Started with " + d.StartCmd + "; set PORT if it does not listen on 3000."
	}
	if d.Strategy == "static" {
		d.Port = 80
	}
	if pm == "bun" && d.Strategy == "node" {
		d.StartCmd = strings.Replace(d.StartCmd, "npm run", "bun run", 1)
	}
	return d
}

func detectPython(dir string, d Detection, marker string) Detection {
	d.Strategy, d.Framework, d.Port = "python", "Python", 8000
	d.InstallCmd = "pip install -r requirements.txt"
	if marker == "pyproject.toml" {
		if _, err := os.Stat(filepath.Join(dir, "uv.lock")); err == nil {
			d.InstallCmd = "uv sync --frozen --no-dev"
		} else if _, err := os.Stat(filepath.Join(dir, "poetry.lock")); err == nil {
			d.InstallCmd = "pip install poetry && poetry install --only main --no-root"
		} else {
			d.InstallCmd = "pip install ."
		}
	} else if marker == "Pipfile" {
		d.InstallCmd = "pip install pipenv && pipenv install --deploy --system"
	}
	content := ""
	for _, f := range []string{"requirements.txt", "pyproject.toml", "Pipfile"} {
		if b, err := os.ReadFile(filepath.Join(dir, f)); err == nil {
			content += strings.ToLower(string(b))
		}
	}
	if b, err := os.ReadFile(filepath.Join(dir, ".python-version")); err == nil {
		d.PyVer = strings.TrimSpace(string(b))
	}
	module := "main:app"
	for _, cand := range []string{"main.py", "app.py", "app/main.py", "src/main.py"} {
		if _, err := os.Stat(filepath.Join(dir, cand)); err == nil {
			module = strings.TrimSuffix(strings.ReplaceAll(cand, "/", "."), ".py") + ":app"
			break
		}
	}
	switch {
	case strings.Contains(content, "django"):
		d.Framework = "Django"
		proj := "config"
		if entries, err := os.ReadDir(dir); err == nil {
			for _, e := range entries {
				if e.IsDir() {
					if _, err := os.Stat(filepath.Join(dir, e.Name(), "wsgi.py")); err == nil {
						proj = e.Name()
					}
				}
			}
		}
		d.StartCmd = "gunicorn " + proj + ".wsgi:application --bind 0.0.0.0:8000 --workers 2"
		d.Summary = "Django project. Served by gunicorn on port 8000. Add a pre-deploy command like python manage.py migrate."
	case strings.Contains(content, "fastapi"):
		d.Framework = "FastAPI"
		d.StartCmd = "uvicorn " + module + " --host 0.0.0.0 --port 8000"
		d.Summary = "FastAPI app. Served by uvicorn on port 8000."
	case strings.Contains(content, "flask"):
		d.Framework = "Flask"
		d.StartCmd = "gunicorn " + module + " --bind 0.0.0.0:8000 --workers 2"
		d.Summary = "Flask app. Served by gunicorn on port 8000."
	default:
		d.StartCmd = "python main.py"
		d.Summary = "Python app. Started with python main.py; set the start command if it differs."
	}
	return d
}

// Dockerfile renders the build for strategies without a user Dockerfile.
// buildEnv holds the variables baked into the image (NEXT_PUBLIC_*, VITE_*).
func Dockerfile(a *App, buildEnv []string) string {
	var b strings.Builder
	args := func() {
		for _, kv := range buildEnv {
			k, _, _ := strings.Cut(kv, "=")
			fmt.Fprintf(&b, "ARG %s\nENV %s=$%s\n", k, k, k)
		}
	}
	node := "22"
	if a.NodeVer != "" {
		node = a.NodeVer
	}
	py := "3.12"
	if a.PyVer != "" {
		py = a.PyVer
	}
	switch a.Strategy {
	case "static":
		if a.BuildCmd == "" {
			b.WriteString("FROM nginx:1.27-alpine\nCOPY . /usr/share/nginx/html\n")
		} else {
			fmt.Fprintf(&b, "FROM node:%s-alpine AS build\nWORKDIR /app\n", node)
			if strings.HasPrefix(a.InstallCmd, "pnpm") {
				b.WriteString("RUN corepack enable\n")
			} else if strings.HasPrefix(a.InstallCmd, "bun") {
				b.WriteString("RUN npm i -g bun\n")
			}
			b.WriteString("COPY package*.json pnpm-lock.yaml* yarn.lock* bun.lockb* ./\n")
			fmt.Fprintf(&b, "RUN %s\nCOPY . .\n", a.InstallCmd)
			args()
			fmt.Fprintf(&b, "RUN %s\n\nFROM nginx:1.27-alpine\nCOPY --from=build /app/%s /usr/share/nginx/html\n", a.BuildCmd, strings.TrimPrefix(strings.TrimSuffix(a.OutputDir, "/"), "./"))
		}
		b.WriteString("COPY .islet/nginx.conf /etc/nginx/conf.d/default.conf\nEXPOSE 80\n")
	case "node":
		fmt.Fprintf(&b, "FROM node:%s-alpine\nWORKDIR /app\nENV NODE_ENV=production\n", node)
		if strings.HasPrefix(a.InstallCmd, "pnpm") {
			b.WriteString("RUN corepack enable\n")
		} else if strings.HasPrefix(a.InstallCmd, "bun") {
			b.WriteString("RUN npm i -g bun\n")
		}
		b.WriteString("COPY package*.json pnpm-lock.yaml* yarn.lock* bun.lockb* ./\n")
		install := a.InstallCmd
		if strings.HasPrefix(install, "npm ci") || strings.HasPrefix(install, "npm install") {
			install += " --include=dev"
		}
		fmt.Fprintf(&b, "RUN %s\nCOPY . .\n", install)
		args()
		if a.BuildCmd != "" {
			fmt.Fprintf(&b, "RUN %s\n", a.BuildCmd)
		}
		fmt.Fprintf(&b, "ENV PORT=%d\nEXPOSE %d\nCMD %s\n", a.Port, a.Port, shellCmd(a.StartCmd))
	case "python":
		fmt.Fprintf(&b, "FROM python:%s-slim\nWORKDIR /app\nENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1 PIP_NO_CACHE_DIR=1\n", py)
		if strings.HasPrefix(a.InstallCmd, "uv ") {
			b.WriteString("COPY --from=ghcr.io/astral-sh/uv:latest /uv /usr/local/bin/uv\nCOPY pyproject.toml uv.lock* ./\n")
			fmt.Fprintf(&b, "RUN %s\nENV PATH=/app/.venv/bin:$PATH\n", a.InstallCmd)
		} else {
			b.WriteString("COPY requirements.txt* pyproject.toml* poetry.lock* Pipfile* Pipfile.lock* ./\n")
			fmt.Fprintf(&b, "RUN %s\n", a.InstallCmd)
		}
		b.WriteString("RUN pip install gunicorn uvicorn 2>/dev/null || true\nCOPY . .\n")
		args()
		if a.BuildCmd != "" {
			fmt.Fprintf(&b, "RUN %s\n", a.BuildCmd)
		}
		fmt.Fprintf(&b, "ENV PORT=%d\nEXPOSE %d\nCMD %s\n", a.Port, a.Port, shellCmd(a.StartCmd))
	case "go":
		b.WriteString("FROM golang:1.24-alpine AS build\nWORKDIR /src\nCOPY go.mod go.sum* ./\nRUN go mod download\nCOPY . .\n")
		args()
		fmt.Fprintf(&b, "RUN CGO_ENABLED=0 %s\n\nFROM alpine:3\nRUN apk add --no-cache ca-certificates tzdata\nWORKDIR /app\nCOPY --from=build /app /app\nENV PORT=%d\nEXPOSE %d\nCMD %s\n", a.BuildCmd, a.Port, a.Port, shellCmd(a.StartCmd))
	}
	return b.String()
}

func shellCmd(s string) string {
	q, _ := json.Marshal([]string{"sh", "-c", s})
	return string(q)
}

// NginxConf serves a static build with SPA fallback, hashed-asset caching
// and Netlify-style _redirects.
func NginxConf(spa bool, redirects string) string {
	var b strings.Builder
	b.WriteString(`server {
    listen 80;
    server_name _;
    root /usr/share/nginx/html;
    index index.html;
    gzip on;
    gzip_types text/plain text/css application/json application/javascript image/svg+xml font/woff2;
    gzip_min_length 1024;
    error_page 404 /404.html;
    location ~* \.(?:js|css|woff2?|ttf|png|jpg|jpeg|gif|webp|avif|svg|ico)$ {
        expires 30d;
        add_header Cache-Control "public, max-age=2592000, immutable";
        try_files $uri =404;
    }
`)
	for _, line := range strings.Split(redirects, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || strings.HasPrefix(f[0], "#") {
			continue
		}
		code := "301"
		if len(f) >= 3 {
			code = f[2]
		}
		from, to := f[0], f[1]
		if strings.HasSuffix(from, "/*") {
			fmt.Fprintf(&b, "    location ^~ %s/ { ", strings.TrimSuffix(from, "/*"))
		} else {
			fmt.Fprintf(&b, "    location = %s { ", from)
		}
		if code == "200" {
			fmt.Fprintf(&b, "try_files %s =404; }\n", strings.ReplaceAll(to, ":splat", "$1"))
		} else {
			fmt.Fprintf(&b, "return %s %s; }\n", code, strings.ReplaceAll(to, "/:splat", "$request_uri"))
		}
	}
	if spa {
		b.WriteString("    location / {\n        try_files $uri $uri/ /index.html;\n    }\n")
	} else {
		b.WriteString("    location / {\n        try_files $uri $uri/ =404;\n    }\n")
	}
	b.WriteString("}\n")
	return b.String()
}
