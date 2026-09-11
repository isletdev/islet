package deploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectFrameworks(t *testing.T) {
	cases := []struct {
		files     map[string]string
		strategy  string
		framework string
		port      int
	}{
		{map[string]string{"composer.json": "{}", "artisan": "", "public/index.php": ""}, "php", "Laravel", 8080},
		{map[string]string{"composer.json": "{}", "index.php": ""}, "php", "PHP", 8080},
		{map[string]string{"Gemfile": "", "bin/rails": "", "config.ru": ""}, "ruby", "Rails", 3000},
		{map[string]string{"Gemfile": "", "config.ru": ""}, "ruby", "Ruby", 3000},
		{map[string]string{"Cargo.toml": "[package]\nname = \"srv\""}, "rust", "Rust", 8080},
		{map[string]string{"pom.xml": "<project/>"}, "java", "Java", 8080},
		{map[string]string{"build.gradle.kts": ""}, "java", "Java", 8080},
		{map[string]string{"Web.csproj": "<Project/>"}, "dotnet", ".NET", 8080},
	}
	for _, c := range cases {
		dir := t.TempDir()
		for name, body := range c.files {
			p := filepath.Join(dir, name)
			_ = os.MkdirAll(filepath.Dir(p), 0o755)
			_ = os.WriteFile(p, []byte(body), 0o644)
		}
		d := Detect(dir)
		if d.Strategy != c.strategy || d.Framework != c.framework || d.Port != c.port {
			t.Errorf("%v: got %s/%s/%d want %s/%s/%d", c.files, d.Strategy, d.Framework, d.Port, c.strategy, c.framework, c.port)
		}
		df := Dockerfile(&App{Strategy: d.Strategy, InstallCmd: d.InstallCmd, BuildCmd: d.BuildCmd, StartCmd: d.StartCmd, Port: d.Port, RootDir: dir}, nil)
		if !strings.HasPrefix(df, "FROM ") || !strings.Contains(df, "EXPOSE ") {
			t.Errorf("%s: bad Dockerfile:\n%s", c.strategy, df)
		}
	}
	if d := Detect(t.TempDir()); d.Strategy == "dotnet" {
		t.Fatal("empty dir should not detect .NET")
	}
	// .NET start command names the project dll.
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "Shop.Api.csproj"), []byte("<Project/>"), 0o644)
	if d := Detect(dir); d.StartCmd != "dotnet /app/out/Shop.Api.dll" {
		t.Fatalf("dotnet start: %s", d.StartCmd)
	}
}
