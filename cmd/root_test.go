package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func executeRoot(t *testing.T, args ...string) error {
	t.Helper()
	if err := importCmd.Flags().Set("mbox", ""); err != nil {
		t.Fatalf("reset import mbox flag: %v", err)
	}
	if err := importCmd.Flags().Set("vault", ""); err != nil {
		t.Fatalf("reset import vault flag: %v", err)
	}
	if err := serveCmd.Flags().Set("addr", "127.0.0.1:8080"); err != nil {
		t.Fatalf("reset serve addr flag: %v", err)
	}
	if err := serveCmd.Flags().Set("vault", ""); err != nil {
		t.Fatalf("reset serve vault flag: %v", err)
	}

	rootCmd.SetArgs(args)
	return rootCmd.Execute()
}

func TestRootHasSubcommands(t *testing.T) {
	foundServe := false
	foundImport := false
	for _, cmd := range rootCmd.Commands() {
		switch cmd.Name() {
		case "serve":
			foundServe = true
		case "import":
			foundImport = true
		}
	}

	if !foundServe {
		t.Fatalf("expected root command to include serve subcommand")
	}
	if !foundImport {
		t.Fatalf("expected root command to include import subcommand")
	}
}

func TestImportRequiresMbox(t *testing.T) {
	if err := executeRoot(t, "import", "--vault", "/tmp/vault"); err == nil {
		t.Fatalf("expected error when --mbox is missing")
	}
}

func TestImportRunsWithMbox(t *testing.T) {
	vaultDir := t.TempDir()
	mboxPath := filepath.Join(vaultDir, "test.mbox")
	if err := os.WriteFile(mboxPath, []byte(""), 0o600); err != nil {
		t.Fatalf("write temp mbox: %v", err)
	}

	if err := executeRoot(t, "import", "--vault", vaultDir, "--mbox", mboxPath); err != nil {
		t.Fatalf("expected import to succeed, got: %v", err)
	}
}

func TestServeDefaultAddrFlag(t *testing.T) {
	flag := serveCmd.Flags().Lookup("addr")
	if flag == nil {
		t.Fatalf("serve command missing addr flag")
	}
	if flag.DefValue != "127.0.0.1:8080" {
		t.Fatalf("unexpected default addr: %s", flag.DefValue)
	}
}

func TestServeRequiresVault(t *testing.T) {
	if err := executeRoot(t, "serve"); err == nil {
		t.Fatalf("expected error when --vault is missing")
	}
}

func TestRunImportEmpty(t *testing.T) {
	if err := runImport("", "/tmp/test.mbox", false, false, ""); err == nil {
		t.Fatalf("expected error for empty vault path")
	}
	if err := runImport("/tmp/vault", "", false, false, ""); err == nil {
		t.Fatalf("expected error for empty mbox path")
	}
}
