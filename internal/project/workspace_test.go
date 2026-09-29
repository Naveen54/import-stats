package project

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeWorkspaceFile(t *testing.T, root, name, body string) string {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func mkdirWorkspaceDir(t *testing.T, root, name string) string {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func workspacePackageNames(pkgs []Package) []string {
	names := make([]string, len(pkgs))
	for i, pkg := range pkgs {
		names[i] = pkg.Name
	}
	return names
}

func TestDetectWorkspaceNPMWorkspacesArray(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, "package.json", `{"workspaces":["packages/*","libs/*/core","missing/*"]}`)
	writeWorkspaceFile(t, root, "packages/ui/package.json", `{"name":"@acme/ui","main":"dist/index.js"}`)
	main := writeWorkspaceFile(t, root, "packages/ui/dist/index.js", "")
	writeWorkspaceFile(t, root, "packages/no-name/package.json", `{"version":"1.0.0"}`)
	mkdirWorkspaceDir(t, root, "packages/no-package")
	writeWorkspaceFile(t, root, "libs/design/core/package.json", `{"name":"@acme/design-core","module":"src/main.ts"}`)
	module := writeWorkspaceFile(t, root, "libs/design/core/src/main.ts", "")

	ws, err := DetectWorkspace(filepath.Join(root, "packages", "ui", "src", "deep"))
	if err != nil {
		t.Fatal(err)
	}
	if ws == nil {
		t.Fatal("workspace = nil")
	}
	if ws.Root != root || ws.Manager != "npm" {
		t.Fatalf("root/manager = %q/%q, want %q/npm", ws.Root, ws.Manager, root)
	}
	if got, want := workspacePackageNames(ws.Packages), []string{"@acme/design-core", "@acme/ui"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("packages = %v, want %v", got, want)
	}
	if ws.Packages[0].Main != module || ws.Packages[1].Main != main {
		t.Fatalf("main files = %q/%q, want %q/%q", ws.Packages[0].Main, ws.Packages[1].Main, module, main)
	}
}

func TestDetectWorkspaceYarnWorkspacesObject(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, "package.json", `{"workspaces":{"packages":["apps/*","packages/*"],"nohoist":["**/react"]}}`)
	writeWorkspaceFile(t, root, "apps/web/package.json", `{"name":"web","exports":{"import":"src/exported.ts"}}`)
	exported := writeWorkspaceFile(t, root, "apps/web/src/exported.ts", "")
	writeWorkspaceFile(t, root, "packages/ui/package.json", `{"name":"ui"}`)
	fallback := writeWorkspaceFile(t, root, "packages/ui/src/index.tsx", "")

	ws, err := DetectWorkspace(filepath.Join(root, "apps", "web"))
	if err != nil {
		t.Fatal(err)
	}
	if ws == nil {
		t.Fatal("workspace = nil")
	}
	if ws.Manager != "yarn" {
		t.Fatalf("manager = %q, want yarn", ws.Manager)
	}
	if got, want := workspacePackageNames(ws.Packages), []string{"ui", "web"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("packages = %v, want %v", got, want)
	}
	if ws.Packages[0].Main != fallback || ws.Packages[1].Main != exported {
		t.Fatalf("main files = %q/%q, want %q/%q", ws.Packages[0].Main, ws.Packages[1].Main, fallback, exported)
	}
}

func TestDetectWorkspacePNPMYAMLCommentsQuotesBlankLinesAndNegation(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, "pnpm-workspace.yaml", `
# top-level comment
packages:
  - 'packages/*' # quoted include

   - "components/*"
  - !packages/excluded
`)
	writeWorkspaceFile(t, root, "packages/app/package.json", `{"name":"app"}`)
	writeWorkspaceFile(t, root, "packages/excluded/package.json", `{"name":"excluded"}`)
	writeWorkspaceFile(t, root, "components/button/package.json", `{"name":"button"}`)

	ws, err := DetectWorkspace(filepath.Join(root, "packages", "app"))
	if err != nil {
		t.Fatal(err)
	}
	if ws == nil {
		t.Fatal("workspace = nil")
	}
	if ws.Manager != "pnpm" {
		t.Fatalf("manager = %q, want pnpm", ws.Manager)
	}
	if got, want := workspacePackageNames(ws.Packages), []string{"app", "button"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("packages = %v, want %v", got, want)
	}
}

func TestDetectWorkspaceLernaJSON(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, "lerna.json", `{"packages":["packages/*"]}`)
	writeWorkspaceFile(t, root, "packages/a/package.json", `{"name":"a","exports":{".":{"default":"lib/index"}}}`)
	main := writeWorkspaceFile(t, root, "packages/a/lib/index.js", "")

	ws, err := DetectWorkspace(filepath.Join(root, "packages", "a"))
	if err != nil {
		t.Fatal(err)
	}
	if ws == nil {
		t.Fatal("workspace = nil")
	}
	if ws.Manager != "lerna" {
		t.Fatalf("manager = %q, want lerna", ws.Manager)
	}
	if got, want := workspacePackageNames(ws.Packages), []string{"a"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("packages = %v, want %v", got, want)
	}
	if ws.Packages[0].Main != main {
		t.Fatalf("main = %q, want %q", ws.Packages[0].Main, main)
	}
}

func TestDetectWorkspaceDuplicateNamesKeepFirstDirectorySorted(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, "package.json", `{"workspaces":["packages/*"]}`)
	writeWorkspaceFile(t, root, "packages/a/package.json", `{"name":"same"}`)
	writeWorkspaceFile(t, root, "packages/b/package.json", `{"name":"same"}`)

	ws, err := DetectWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(ws.Packages), 1; got != want {
		t.Fatalf("package count = %d, want %d", got, want)
	}
	if ws.Packages[0].Dir != filepath.Join(root, "packages", "a") {
		t.Fatalf("kept dir = %q, want first sorted dir", ws.Packages[0].Dir)
	}
	if got, want := ws.DuplicateNames, []string{"same"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("duplicates = %v, want %v", got, want)
	}
}

func TestDetectWorkspaceNoWorkspace(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, "package.json", `{"name":"single-app"}`)
	ws, err := DetectWorkspace(filepath.Join(root, "src", "deep"))
	if err != nil {
		t.Fatal(err)
	}
	if ws != nil {
		t.Fatalf("workspace = %+v, want nil", ws)
	}
}

func TestDetectWorkspaceNestedStartSeveralLevelsBelowRoot(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, "package.json", `{"workspaces":["packages/*"]}`)
	writeWorkspaceFile(t, root, "packages/app/package.json", `{"name":"app"}`)

	start := mkdirWorkspaceDir(t, root, "packages/app/src/features/home")
	ws, err := DetectWorkspace(start)
	if err != nil {
		t.Fatal(err)
	}
	if ws == nil || ws.Root != root {
		t.Fatalf("workspace root = %+v, want %q", ws, root)
	}
}

func TestDetectWorkspaceDeterministicSortedPackages(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, "package.json", `{"workspaces":["z/*","a/*","m/*"]}`)
	writeWorkspaceFile(t, root, "z/one/package.json", `{"name":"zeta"}`)
	writeWorkspaceFile(t, root, "a/one/package.json", `{"name":"alpha"}`)
	writeWorkspaceFile(t, root, "m/one/package.json", `{"name":"middle"}`)

	want := []string{"alpha", "middle", "zeta"}
	for i := 0; i < 5; i++ {
		ws, err := DetectWorkspace(root)
		if err != nil {
			t.Fatal(err)
		}
		if got := workspacePackageNames(ws.Packages); !reflect.DeepEqual(got, want) {
			t.Fatalf("run %d packages = %v, want %v", i, got, want)
		}
	}
}

func TestDetectWorkspaceSkipsNodeModulesDecoys(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, "package.json", `{"workspaces":["**"]}`)
	writeWorkspaceFile(t, root, "packages/app/package.json", `{"name":"app"}`)
	writeWorkspaceFile(t, root, "node_modules/decoy/package.json", `{"name":"decoy"}`)
	writeWorkspaceFile(t, root, "packages/app/node_modules/nested-decoy/package.json", `{"name":"nested-decoy"}`)

	ws, err := DetectWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := workspacePackageNames(ws.Packages), []string{"app"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("packages = %v, want %v", got, want)
	}
}

func TestDetectWorkspaceDoubleStarFindsNestedPackages(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, "package.json", `{"workspaces":["apps/**"]}`)
	writeWorkspaceFile(t, root, "apps/web/package.json", `{"name":"web"}`)
	writeWorkspaceFile(t, root, "apps/group/admin/package.json", `{"name":"admin"}`)

	ws, err := DetectWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := workspacePackageNames(ws.Packages), []string{"admin", "web"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("packages = %v, want %v", got, want)
	}
}

func TestDetectWorkspaceMalformedInputsDoNotPanic(t *testing.T) {
	t.Run("malformed root package json", func(t *testing.T) {
		root := t.TempDir()
		writeWorkspaceFile(t, root, "package.json", `{"workspaces":`)
		ws, err := DetectWorkspace(root)
		if err != nil {
			t.Fatal(err)
		}
		if ws != nil {
			t.Fatalf("workspace = %+v, want nil", ws)
		}
	})

	t.Run("malformed workspace package json", func(t *testing.T) {
		root := t.TempDir()
		writeWorkspaceFile(t, root, "package.json", `{"workspaces":["packages/*"]}`)
		writeWorkspaceFile(t, root, "packages/bad/package.json", `{"name":`)
		ws, err := DetectWorkspace(root)
		if err != nil {
			t.Fatal(err)
		}
		if got := len(ws.Packages); got != 0 {
			t.Fatalf("package count = %d, want 0", got)
		}
	})

	t.Run("malformed yaml", func(t *testing.T) {
		root := t.TempDir()
		writeWorkspaceFile(t, root, "pnpm-workspace.yaml", "packages:\n  not-a-list\n  - 'unterminated\n")
		ws, err := DetectWorkspace(root)
		if err != nil {
			t.Fatal(err)
		}
		if ws == nil {
			t.Fatal("workspace = nil, want tolerant pnpm workspace")
		}
		if got := len(ws.Packages); got != 0 {
			t.Fatalf("package count = %d, want 0", got)
		}
	})
}
