package updates

import (
	"fmt"
	"testing"
)

type stubRunner struct {
	out  map[string]string
	http map[string]string
	err  error
}

func (s stubRunner) Run(cmd string, args ...string) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	key := cmd
	for _, a := range args {
		key += " " + a
	}
	if v, ok := s.out[key]; ok {
		return v, nil
	}
	return "", fmt.Errorf("no stub for %q", key)
}

func (s stubRunner) HTTPGet(url string) ([]byte, error) {
	if s.err != nil {
		return nil, s.err
	}
	if v, ok := s.http[url]; ok {
		return []byte(v), nil
	}
	return nil, fmt.Errorf("no http stub for %q", url)
}

func TestSplitPkgVersion(t *testing.T) {
	cases := []struct {
		in           string
		pkg, version string
	}{
		{"foo", "foo", ""},
		{"foo@1.2.3", "foo", "1.2.3"},
		{"@scope/pkg", "@scope/pkg", ""},
		{"@scope/pkg@1.2.3", "@scope/pkg", "1.2.3"},
		{"pip-pkg==2.0", "pip-pkg", "2.0"},
	}
	for _, tc := range cases {
		gotP, gotV := splitPkgVersion(tc.in)
		if gotP != tc.pkg || gotV != tc.version {
			t.Errorf("splitPkgVersion(%q) = (%q,%q); want (%q,%q)", tc.in, gotP, gotV, tc.pkg, tc.version)
		}
	}
}

func TestDetectMCPLauncher(t *testing.T) {
	cases := []struct {
		name    string
		cmd     string
		args    []string
		wantPkg string
		wantVer string
		wantKnd string
	}{
		{"npx pinned", "npx", []string{"-y", "@scope/foo@1.0.0"}, "@scope/foo", "1.0.0", "npm"},
		{"npx floating", "npx", []string{"-y", "package-name"}, "package-name", "", "npm"},
		{"uvx pinned", "uvx", []string{"my-tool==2.5"}, "my-tool", "2.5", "pypi"},
		{"uv tool run", "uv", []string{"tool", "run", "x@1"}, "x", "1", "pypi"},
		{"absolute npx path", "/usr/local/bin/npx", []string{"-y", "x@1"}, "x", "1", "npm"},
		{"docker — unknown", "docker", []string{"run", "image"}, "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := DetectMCPLauncher(tc.cmd, tc.args)
			if l.Pkg != tc.wantPkg || l.Version != tc.wantVer || l.Kind != tc.wantKnd {
				t.Errorf("got %+v; want pkg=%q ver=%q kind=%q", l, tc.wantPkg, tc.wantVer, tc.wantKnd)
			}
		})
	}
}

func TestCheckMCPLauncher_NPM(t *testing.T) {
	r := stubRunner{out: map[string]string{"npm view foo version": "2.0.0\n"}}
	s := CheckMCPLauncher(r, MCPLauncher{Pkg: "foo", Version: "1.0.0", Kind: "npm"})
	if s.Remote != "2.0.0" {
		t.Errorf("Remote=%q, want 2.0.0", s.Remote)
	}
	if !s.Outdated {
		t.Errorf("expected Outdated=true")
	}
}

func TestCheckMCPLauncher_NPM_UpToDate(t *testing.T) {
	r := stubRunner{out: map[string]string{"npm view foo version": "1.0.0\n"}}
	s := CheckMCPLauncher(r, MCPLauncher{Pkg: "foo", Version: "1.0.0", Kind: "npm"})
	if s.Outdated {
		t.Errorf("expected Outdated=false when versions match")
	}
}

func TestCheckMCPLauncher_FloatingVersion(t *testing.T) {
	r := stubRunner{out: map[string]string{"npm view foo version": "9.9.9"}}
	s := CheckMCPLauncher(r, MCPLauncher{Pkg: "foo", Version: "", Kind: "npm"})
	if s.Outdated {
		t.Errorf("floating version should never be Outdated")
	}
}

func TestCheckMCPLauncher_PyPI(t *testing.T) {
	r := stubRunner{http: map[string]string{
		"https://pypi.org/pypi/widget/json": `{"info":{"version":"3.0.1"}}`,
	}}
	s := CheckMCPLauncher(r, MCPLauncher{Pkg: "widget", Version: "2.0.0", Kind: "pypi"})
	if s.Remote != "3.0.1" {
		t.Errorf("Remote=%q, want 3.0.1", s.Remote)
	}
	if !s.Outdated {
		t.Errorf("expected Outdated=true")
	}
}

func TestCacheCountOutdated(t *testing.T) {
	c := NewCache()
	c.PutMarketplace("a", Status{Outdated: true})
	c.PutMarketplace("b", Status{Outdated: false})
	c.PutPlugin("p1@a", Status{Outdated: true})
	c.PutMCP("m1", Status{Outdated: true})
	c.PutMCP("m2", Status{Outdated: true})
	mkt, plg, mcp := c.CountOutdated()
	if mkt != 1 || plg != 1 || mcp != 2 {
		t.Errorf("got mkt=%d plg=%d mcp=%d; want 1/1/2", mkt, plg, mcp)
	}
}

func TestCacheInvalidate(t *testing.T) {
	c := NewCache()
	c.PutPlugin("x", Status{Outdated: true})
	c.PutMarketplace("y", Status{Outdated: true})
	c.InvalidatePlugin("x")
	c.InvalidateMarketplace("y")
	if _, ok := c.Plugin("x"); ok {
		t.Errorf("plugin not invalidated")
	}
	if _, ok := c.Marketplace("y"); ok {
		t.Errorf("marketplace not invalidated")
	}
}
