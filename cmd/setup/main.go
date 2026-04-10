package main

import (
	"archive/zip"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

const (
	namespace = "automotive"
	rgURL     = "https://github.com/BurntSushi/ripgrep/releases/download/14.1.1/ripgrep-14.1.1-x86_64-pc-windows-msvc.zip"
)

func main() {
	cfg := parseFlags()
	if err := run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

type config struct {
	repoRoot   string
	target     string
	dryRun     bool
	status     bool
	uninstall  bool
	downloadRG bool
	rgZip      string
	manifest   string
}

func parseFlags() config {
	home, _ := os.UserHomeDir()
	defaultTarget := filepath.Join(home, ".codechat")

	var cfg config
	var project string
	flag.StringVar(&cfg.repoRoot, "repo-root", ".", "repository root")
	flag.StringVar(&cfg.target, "target", defaultTarget, "installation target directory")
	flag.StringVar(&project, "project", "", "project directory (installs to <project>/.codechat)")
	flag.BoolVar(&cfg.dryRun, "dry-run", false, "preview changes without writing files")
	flag.BoolVar(&cfg.status, "status", false, "show current installation status")
	flag.BoolVar(&cfg.uninstall, "uninstall", false, "remove previously installed automotive assets")
	flag.BoolVar(&cfg.downloadRG, "download-rg", true, "download rg.exe at runtime on Windows")
	flag.StringVar(&cfg.rgZip, "rg-zip", "", "use local ripgrep zip archive instead of download")
	flag.Parse()

	if project != "" {
		cfg.target = filepath.Join(project, ".codechat")
	}
	cfg.manifest = filepath.Join(cfg.target, ".automotive-manifest")
	return cfg
}

func run(cfg config) error {
	repoRoot, err := filepath.Abs(cfg.repoRoot)
	if err != nil {
		return err
	}
	cfg.repoRoot = repoRoot

	if cfg.status {
		return printStatus(cfg)
	}
	if cfg.uninstall {
		return uninstall(cfg)
	}

	if !cfg.dryRun {
		if err := os.MkdirAll(cfg.target, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(cfg.manifest, nil, 0o644); err != nil {
			return err
		}
	}

	var created []string
	track := func(path string) {
		created = append(created, path)
		if !cfg.dryRun {
			_ = appendManifest(cfg.manifest, path)
		}
	}

	if err := installAgents(cfg, track); err != nil {
		return err
	}
	if err := installCommands(cfg, track); err != nil {
		return err
	}
	if err := installSkills(cfg, track); err != nil {
		return err
	}
	if err := linkDirectory(cfg, "rules", filepath.Join(cfg.target, "rules", namespace), track); err != nil {
		return err
	}
	if err := linkDirectory(cfg, "hooks", filepath.Join(cfg.target, "hooks", namespace), track); err != nil {
		return err
	}
	if err := linkDirectory(cfg, "knowledge-base", filepath.Join(cfg.target, "knowledge-base", namespace), track); err != nil {
		return err
	}
	if err := linkDirectory(cfg, "workflows", filepath.Join(cfg.target, namespace+"-workflows"), track); err != nil {
		return err
	}
	if err := writeSettingsSnippet(cfg, track); err != nil {
		return err
	}
	if err := installRipgrep(cfg, track); err != nil {
		return err
	}

	fmt.Printf("Installed %d automotive components into %s\n", len(created), cfg.target)
	return nil
}

func printStatus(cfg config) error {
	fmt.Printf("Target: %s\n", cfg.target)
	data, err := os.ReadFile(cfg.manifest)
	if errors.Is(err, os.ErrNotExist) {
		fmt.Println("Not installed (manifest missing)")
		return nil
	}
	if err != nil {
		return err
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		lines = nil
	}
	fmt.Printf("Tracked components: %d\n", len(lines))
	for _, p := range lines {
		if p == "" {
			continue
		}
		_, err := os.Stat(p)
		state := "OK"
		if err != nil {
			state = "MISSING"
		}
		fmt.Printf("  [%s] %s\n", state, p)
	}
	return nil
}

func uninstall(cfg config) error {
	data, err := os.ReadFile(cfg.manifest)
	if errors.Is(err, os.ErrNotExist) {
		fmt.Println("No manifest found; nothing to uninstall")
		return nil
	}
	if err != nil {
		return err
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		p := strings.TrimSpace(lines[i])
		if p == "" {
			continue
		}
		_ = os.RemoveAll(p)
	}
	_ = os.Remove(cfg.manifest)
	fmt.Println("Uninstall complete")
	return nil
}

func installAgents(cfg config, track func(string)) error {
	srcRoot := filepath.Join(cfg.repoRoot, "agents")
	dstRoot := filepath.Join(cfg.target, "agents")
	if _, err := os.Stat(srcRoot); err != nil {
		return nil
	}
	if !cfg.dryRun {
		_ = os.MkdirAll(dstRoot, 0o755)
	}

	return filepath.Walk(srcRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || filepath.Ext(path) != ".yaml" {
			return err
		}
		rel, _ := filepath.Rel(srcRoot, path)
		cat := filepath.Base(filepath.Dir(rel))
		base := strings.TrimSuffix(filepath.Base(path), ".yaml")
		dst := filepath.Join(dstRoot, fmt.Sprintf("%s-%s-%s.md", namespace, cat, base))
		if cfg.dryRun {
			return nil
		}
		name, desc, role := parseAgentYAML(path)
		content := fmt.Sprintf("---\nname: %s-%s-%s\ndescription: %q\ntools: Read, Grep, Glob, PowerShell\n---\n\n# Automotive Agent: %s\n\n%s\n", namespace, cat, base, desc, name, role)
		if err := os.WriteFile(dst, []byte(content), 0o644); err != nil {
			return err
		}
		track(dst)
		return nil
	})
}

func installCommands(cfg config, track func(string)) error {
	srcRoot := filepath.Join(cfg.repoRoot, "commands")
	dstRoot := filepath.Join(cfg.target, "commands", namespace)
	if _, err := os.Stat(srcRoot); err != nil {
		return nil
	}
	if !cfg.dryRun {
		_ = os.MkdirAll(dstRoot, 0o755)
	}

	return filepath.Walk(srcRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || filepath.Ext(path) != ".sh" {
			return err
		}
		rel, _ := filepath.Rel(srcRoot, path)
		category := filepath.Base(filepath.Dir(rel))
		base := strings.TrimSuffix(filepath.Base(path), ".sh")
		dst := filepath.Join(dstRoot, fmt.Sprintf("%s-%s.md", category, base))
		if cfg.dryRun {
			return nil
		}
		desc := firstComment(path)
		cmd := fmt.Sprintf("bash %q", path)
		if runtime.GOOS == "windows" {
			cmd = fmt.Sprintf("powershell -Command \"bash '%s'\"", strings.ReplaceAll(path, "\\", "/"))
		}
		body := fmt.Sprintf("---\ndescription: %q\n---\n\n```powershell\n%s\n```\n", desc, cmd)
		if err := os.WriteFile(dst, []byte(body), 0o644); err != nil {
			return err
		}
		track(dst)
		return nil
	})
}

func installSkills(cfg config, track func(string)) error {
	srcRoot := filepath.Join(cfg.repoRoot, "skills")
	dstRoot := filepath.Join(cfg.target, "skills")
	if _, err := os.Stat(srcRoot); err != nil {
		return nil
	}
	if !cfg.dryRun {
		_ = os.MkdirAll(dstRoot, 0o755)
	}

	entries, err := os.ReadDir(srcRoot)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == "_templates" {
			continue
		}
		cat := entry.Name()
		srcCat := filepath.Join(srcRoot, cat)
		dstCat := filepath.Join(dstRoot, namespace+"-"+cat)
		contentDir := filepath.Join(dstCat, "content")
		if cfg.dryRun {
			continue
		}
		if err := copyDir(srcCat, contentDir); err != nil {
			return err
		}
		files, _ := os.ReadDir(contentDir)
		sort.Slice(files, func(i, j int) bool { return files[i].Name() < files[j].Name() })
		var list []string
		for _, f := range files {
			if strings.HasSuffix(f.Name(), ".yaml") || strings.HasSuffix(f.Name(), ".md") {
				list = append(list, "- "+f.Name())
			}
		}
		skillMD := fmt.Sprintf("---\nname: %s-%s\ndescription: Automotive %s domain skill bundle\nallowed-tools: Read, Grep, Glob, PowerShell\n---\n\n# %s Skills\n\n%s\n", namespace, cat, cat, cat, strings.Join(list, "\n"))
		if err := os.WriteFile(filepath.Join(dstCat, "SKILL.md"), []byte(skillMD), 0o644); err != nil {
			return err
		}
		track(dstCat)
	}
	return nil
}

func linkDirectory(cfg config, sourceRel, dest string, track func(string)) error {
	src := filepath.Join(cfg.repoRoot, sourceRel)
	if _, err := os.Stat(src); err != nil {
		return nil
	}
	if cfg.dryRun {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	_ = os.RemoveAll(dest)
	if err := copyDir(src, dest); err != nil {
		return err
	}
	track(dest)
	return nil
}

func writeSettingsSnippet(cfg config, track func(string)) error {
	file := filepath.Join(cfg.target, namespace+"-settings-snippet.json")
	if cfg.dryRun {
		return nil
	}
	content := `{
  "hooks_to_add": {
    "PreToolUse": [
      {
        "matcher": "PowerShell",
        "hooks": [
          {
            "type": "command",
            "command": "powershell -File ~/.codechat/hooks/automotive/pre-commit-misra-check.sh",
            "timeout": 15
          }
        ]
      }
    ]
  },
  "permissions_to_add": {
    "allow": [
      "PowerShell(cppcheck:*)",
      "PowerShell(rg:*)"
    ]
  }
}
`
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		return err
	}
	track(file)
	return nil
}

func installRipgrep(cfg config, track func(string)) error {
	if runtime.GOOS != "windows" || !cfg.downloadRG {
		return nil
	}
	binDir := filepath.Join(cfg.target, "bin")
	if cfg.dryRun {
		return nil
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return err
	}
	rgDest := filepath.Join(binDir, "rg.exe")
	if err := extractRipgrep(rgDest, cfg.rgZip); err != nil {
		return err
	}
	track(rgDest)
	return nil
}

func extractRipgrep(dest, localZip string) error {
	zipPath := localZip
	if zipPath == "" {
		tmp, err := os.CreateTemp("", "rg-*.zip")
		if err != nil {
			return err
		}
		defer os.Remove(tmp.Name())
		defer tmp.Close()
		resp, err := http.Get(rgURL)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("ripgrep download failed: %s", resp.Status)
		}
		if _, err := io.Copy(tmp, resp.Body); err != nil {
			return err
		}
		zipPath = tmp.Name()
	}
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()
	for _, f := range r.File {
		if strings.HasSuffix(strings.ToLower(f.Name), "/rg.exe") {
			rc, err := f.Open()
			if err != nil {
				return err
			}
			defer rc.Close()
			out, err := os.Create(dest)
			if err != nil {
				return err
			}
			defer out.Close()
			_, err = io.Copy(out, rc)
			return err
		}
	}
	return errors.New("rg.exe not found in ripgrep zip")
}

func appendManifest(manifest, path string) error {
	f, err := os.OpenFile(manifest, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintln(f, path)
	return err
}

func parseAgentYAML(path string) (name, desc, role string) {
	data, _ := os.ReadFile(path)
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "name:") && name == "" {
			name = strings.Trim(strings.TrimSpace(strings.TrimPrefix(t, "name:")), "\"")
		}
		if strings.HasPrefix(t, "description:") && desc == "" {
			desc = strings.Trim(strings.TrimSpace(strings.TrimPrefix(t, "description:")), "\"")
		}
		if strings.HasPrefix(t, "role:") || strings.HasPrefix(t, "system_prompt:") {
			role = strings.Join(lines[i+1:], "\n")
			break
		}
	}
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	if desc == "" {
		desc = "Automotive agent"
	}
	if role == "" {
		role = "Automotive specialist agent."
	}
	return
}

func firstComment(path string) string {
	data, _ := os.ReadFile(path)
	for _, line := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "#") {
			return strings.TrimSpace(strings.TrimPrefix(t, "#"))
		}
	}
	return "Automotive command"
}

func copyDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.Create(target)
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, in)
		return err
	})
}
