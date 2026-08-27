//go:build lemmary_exttest

package boot

import (
	"path/filepath"
	"testing"
)

// Prepare's contract is what main.go acts on, and main.go has no test of its
// own — it is a process. These assert the contract directly.
//
//	go test -tags lemmary_exttest ./internal/boot/

func TestPrepareHandlesASubcommandWithoutAnApp(t *testing.T) {
	res, err := Prepare([]string{ExtTestSubcommand})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if !res.Handled {
		t.Fatal("subcommand was not handled; main would have constructed an app and opened the database")
	}
	if res.Code != ExtTestHandledCode {
		t.Fatalf("exit code = %d, want %d", res.Code, ExtTestHandledCode)
	}
	// Cleanup has to run on the handled path too, which is the case a
	// `return` before the app exists is most likely to skip.
	if res.Close == nil {
		t.Fatal("handled result carried no Close")
	}
}

func TestPrepareRelocatesTheDataDir(t *testing.T) {
	dir := t.TempDir()

	res, err := Prepare([]string{"serve", "--dir", dir})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if res.Handled {
		t.Fatal("serve was handled; the app would never be constructed")
	}
	want := filepath.Join(dir, ExtTestDataDirName)
	if res.DataDir != want {
		t.Fatalf("DataDir = %q, want %q", res.DataDir, want)
	}
	if res.Register == nil {
		t.Fatal("result carried no Register; main would wire nothing")
	}
}

func TestCloseRuns(t *testing.T) {
	before := ExtTestCloseCount()

	res, err := Prepare([]string{"serve", "--dir", t.TempDir()})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := res.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if got := ExtTestCloseCount() - before; got != 1 {
		t.Fatalf("Close ran %d times, want 1", got)
	}
}

func TestNameIdentifiesTheBuild(t *testing.T) {
	if Name() != "exttest" {
		t.Fatalf("Name() = %q, want %q", Name(), "exttest")
	}
}
