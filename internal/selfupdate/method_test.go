package selfupdate

import (
	"runtime"
	"strings"
	"testing"
)

// TestDetectMethodFromExePath pins install-method detection from the exe path
// alone, including Windows spellings. Scoop and winget are matched case- and
// slash-insensitively because Windows paths are both.
func TestDetectMethodFromExePath(t *testing.T) {
	cases := []struct {
		exe  string
		want Method
	}{
		{"/opt/homebrew/Cellar/ccmcp/0.23.0/bin/ccmcp", MethodBrew},
		{"/home/linuxbrew/.linuxbrew/bin/ccmcp", MethodBrew},
		{"/Users/x/go/bin/ccmcp", MethodGo},
		{"c:/users/x/SCOOP/APPS/ccmcp/0.24.0/ccmcp.exe", MethodScoop},
		{"/usr/local/bin/ccmcp", MethodBinary},
	}
	// Backslash spellings only reach this function on Windows, where
	// os.Executable returns them. Elsewhere a backslash is not a separator.
	if runtime.GOOS == "windows" {
		cases = append(cases, []struct {
			exe  string
			want Method
		}{
			{`C:\Users\x\go\bin\ccmcp.exe`, MethodGo},
			{`C:\Users\x\scoop\apps\ccmcp\current\ccmcp.exe`, MethodScoop},
			{`C:\Users\x\AppData\Local\Microsoft\WinGet\Packages\Robworks.ccmcp_Microsoft.Winget.Source_8wekyb3d8bbwe\ccmcp.exe`, MethodWinget},
			{`C:\Users\x\bin\ccmcp.exe`, MethodBinary},
		}...)
	}
	for _, c := range cases {
		if got := detectMethodFromExe(c.exe); got != c.want {
			t.Fatalf("detectMethodFromExe(%q) = %q, want %q", c.exe, got, c.want)
		}
	}
}

func TestUpgradeCommandForWindowsMethods(t *testing.T) {
	if got := UpgradeCommand(MethodScoop); len(got) != 3 || got[0] != "scoop" || got[1] != "update" || got[2] != "ccmcp" {
		t.Fatalf("scoop upgrade command = %v", got)
	}
	got := UpgradeCommand(MethodWinget)
	if len(got) != 4 || got[0] != "winget" || got[1] != "upgrade" || got[2] != "--id" || got[3] != "Robworks.ccmcp" {
		t.Fatalf("winget upgrade command = %v", got)
	}
	for _, m := range []Method{MethodScoop, MethodWinget} {
		if hint := MethodHint(m, ""); !strings.Contains(hint, string(m)) {
			t.Fatalf("MethodHint(%q) = %q does not name the method", m, hint)
		}
	}
}
