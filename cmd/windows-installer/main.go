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
	"strings"
)

const rgURL = "https://github.com/BurntSushi/ripgrep/releases/download/14.1.1/ripgrep-14.1.1-x86_64-pc-windows-msvc.zip"

var includePaths = []string{
	"agents",
	"commands",
	"hooks",
	"knowledge-base",
	"rules",
	"skills",
	"workflows",
	"README.md",
	"CLAUDE.md",
}

func main() {
	var output string
	var repoRoot string
	var includeRG bool
	var rgZip string

	flag.StringVar(&output, "output", "dist/automotive-codechat-windows.zip", "output zip path")
	flag.StringVar(&repoRoot, "repo-root", ".", "repository root to package")
	flag.BoolVar(&includeRG, "include-rg", true, "download and include rg.exe in package")
	flag.StringVar(&rgZip, "rg-zip", "", "optional local path to ripgrep Windows zip (skips download)")
	flag.Parse()

	if err := run(output, repoRoot, includeRG, rgZip); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Created Windows package: %s\n", output)
}

func run(output, repoRoot string, includeRG bool, rgZip string) error {
	repoRoot, err := filepath.Abs(repoRoot)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return err
	}

	outFile, err := os.Create(output)
	if err != nil {
		return err
	}
	defer outFile.Close()

	zw := zip.NewWriter(outFile)
	defer zw.Close()

	for _, rel := range includePaths {
		abs := filepath.Join(repoRoot, rel)
		if err := addPath(zw, repoRoot, abs); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return err
		}
	}

	if err := addInstallPS1(zw); err != nil {
		return err
	}

	if includeRG {
		if err := addRipgrepBinary(zw, rgZip); err != nil {
			return err
		}
	}

	return nil
}

func addPath(zw *zip.Writer, root, abs string) error {
	info, err := os.Stat(abs)
	if err != nil {
		return err
	}

	if !info.IsDir() {
		return addFile(zw, root, abs)
	}

	return filepath.Walk(abs, func(path string, entry os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		return addFile(zw, root, path)
	})
}

func addFile(zw *zip.Writer, root, path string) error {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return err
	}
	rel = filepath.ToSlash(rel)

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w, err := zw.Create(rel)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, f)
	return err
}

func addInstallPS1(zw *zip.Writer) error {
	const script = `<#
Automotive CodeChat Windows setup script.
Usage:
  powershell -ExecutionPolicy Bypass -File .\install.ps1
#>

$ErrorActionPreference = "Stop"
$repoRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
$target = Join-Path $HOME ".codechat"
$namespace = "automotive"

New-Item -ItemType Directory -Force -Path $target | Out-Null

$dirs = @("agents", "commands", "hooks", "knowledge-base", "rules", "skills", "workflows")
foreach ($dir in $dirs) {
  $source = Join-Path $repoRoot $dir
  if (-Not (Test-Path $source)) { continue }

  $dest = Join-Path $target $dir
  New-Item -ItemType Directory -Force -Path $dest | Out-Null

  $linkPath = Join-Path $dest $namespace
  if (Test-Path $linkPath) { Remove-Item -Recurse -Force $linkPath }
  New-Item -ItemType SymbolicLink -Path $linkPath -Target $source | Out-Null
}

$binDir = Join-Path $target "bin"
New-Item -ItemType Directory -Force -Path $binDir | Out-Null
$rgSource = Join-Path $repoRoot "bin/rg.exe"
if (Test-Path $rgSource) {
  Copy-Item -Force $rgSource (Join-Path $binDir "rg.exe")
}

Write-Host "Installed automotive content to $target" -ForegroundColor Green
`
	w, err := zw.Create("install.ps1")
	if err != nil {
		return err
	}
	_, err = io.Copy(w, strings.NewReader(script))
	return err
}

func addRipgrepBinary(zw *zip.Writer, localZip string) error {
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
			return fmt.Errorf("failed to download ripgrep archive: %s (or pass --rg-zip <path>)", resp.Status)
		}

		if _, err := io.Copy(tmp, resp.Body); err != nil {
			return err
		}
		if err := tmp.Close(); err != nil {
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

			w, err := zw.Create("bin/rg.exe")
			if err != nil {
				return err
			}
			_, err = io.Copy(w, rc)
			return err
		}
	}

	return errors.New("rg.exe not found in ripgrep archive")
}
