package waf

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestIsExecutableRejectsBareNameNotInPATH(t *testing.T) {
	name := "google-chrome"
	if _, err := exec.LookPath(name); err == nil {
		t.Skip(name + " is in PATH")
	}
	if isExecutable(name) {
		t.Fatalf("isExecutable(%q) = true, want false when not in PATH", name)
	}
}

func TestIsExecutableAcceptsBareNameInPATH(t *testing.T) {
	name := "sh"
	if runtime.GOOS == "windows" {
		name = "cmd.exe"
	}
	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("%s not in PATH: %v", name, err)
	}
	if !isExecutable(name) {
		t.Fatalf("isExecutable(%q) = false, want true", name)
	}
}

func TestIsExecutableFileExists(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "chrome-bin")
	if err := os.WriteFile(p, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !isExecutable(p) {
		t.Fatalf("isExecutable(%q) = false, want true", p)
	}
}

func TestIsExecutableMissingAbsPath(t *testing.T) {
	p := filepath.Join(t.TempDir(), "missing-chrome")
	if isExecutable(p) {
		t.Fatalf("isExecutable(%q) = true, want false", p)
	}
}

func TestIsExecutableEmpty(t *testing.T) {
	if isExecutable("") {
		t.Fatal("isExecutable(\"\") = true, want false")
	}
}

func TestLocateBrowserOnceDoesNotReturnMissingBareName(t *testing.T) {
	t.Setenv("KIRO_BROWSER_PATH", "")
	got := locateBrowserOnce()
	if got == "" {
		t.Skip("no Chrome/Edge installed")
	}
	if filepath.Base(got) == got && !filepath.IsAbs(got) {
		if _, err := exec.LookPath(got); err != nil {
			t.Fatalf("locateBrowserOnce returned %q which is not in PATH: %v", got, err)
		}
		return
	}
	if _, err := os.Stat(got); err != nil {
		t.Fatalf("locateBrowserOnce returned %q: %v", got, err)
	}
}

func TestDarwinCandidatesPreferAppBundles(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin only")
	}
	paths := candidatePaths()
	want := "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
	appIdx, bareIdx := -1, -1
	for i, p := range paths {
		if p == want {
			appIdx = i
		}
		if p == "google-chrome" {
			bareIdx = i
		}
	}
	if appIdx < 0 {
		t.Fatalf("darwin candidates missing %q: %v", want, paths)
	}
	if bareIdx >= 0 && appIdx > bareIdx {
		t.Fatalf("app bundle must precede bare name; idx app=%d bare=%d", appIdx, bareIdx)
	}
}

func TestLocateBrowserOnceFindsInstalledChrome(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin only")
	}
	chrome := "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
	if _, err := os.Stat(chrome); err != nil {
		t.Skip("Chrome.app not installed")
	}
	t.Setenv("KIRO_BROWSER_PATH", "")
	got := locateBrowserOnce()
	if got != chrome {
		t.Fatalf("locateBrowserOnce() = %q, want %q", got, chrome)
	}
}

func TestBrowserSessionCloseIdempotent(t *testing.T) {
	// 测试多次调用 Close 不会 panic
	callCount := 0
	s := &BrowserSession{
		taskCancel: func() {
			callCount++
		},
		ctxCancel: func() {
			callCount++
		},
		allocCancel: func() {
			callCount++
		},
	}

	// 第一次调用
	s.Close()
	if callCount != 3 {
		t.Fatalf("expected 3 cancel calls, got %d", callCount)
	}

	// 第二次调用不应该再执行 cancel
	s.Close()
	if callCount != 3 {
		t.Fatalf("expected still 3 cancel calls after second Close, got %d", callCount)
	}

	// 第三次调用也不应该再执行 cancel
	s.Close()
	if callCount != 3 {
		t.Fatalf("expected still 3 cancel calls after third Close, got %d", callCount)
	}
}

