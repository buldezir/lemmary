package appargs

import (
	"slices"
	"testing"
)

// Argv scanning decides what runs before cobra exists. Getting it wrong either
// skips a pre-boot step entirely or points it at an address the browser will
// never look at, so the conventions cobra uses are pinned here.
func TestServeDetectionAndAddress(t *testing.T) {
	cases := []struct {
		name  string
		args  []string
		serve bool
		addr  string
	}{
		{"no arguments", nil, true, "127.0.0.1:8090"},
		{"bare serve", []string{"serve"}, true, "127.0.0.1:8090"},
		{"separate http value", []string{"serve", "--http", "0.0.0.0:80"}, true, "0.0.0.0:80"},
		{"joined http value", []string{"serve", "--http=0.0.0.0:8080"}, true, "0.0.0.0:8080"},
		{"http before subcommand", []string{"--http", "0.0.0.0:80", "serve"}, true, "0.0.0.0:80"},
		// The regression this exists for: without a table of value-taking
		// flags, "0.0.0.0:80" is read as the subcommand and serve is missed.
		{"http value is not the subcommand", []string{"--http", "0.0.0.0:80"}, true, "0.0.0.0:80"},
		{"bool flag then serve", []string{"--dev", "serve"}, true, "127.0.0.1:8090"},
		{"publicDir value", []string{"--publicDir", "serve", "serve"}, true, "127.0.0.1:8090"},
		{"migrate is not serve", []string{"migrate", "up"}, false, "127.0.0.1:8090"},
		{"superuser is not serve", []string{"superuser", "upsert", "a@b.c", "pw"}, false, "127.0.0.1:8090"},
		{"domain args switch to port 80", []string{"serve", "example.com"}, true, "0.0.0.0:80"},
		{"explicit http beats domain args", []string{"serve", "example.com", "--http", "127.0.0.1:9000"}, true, "127.0.0.1:9000"},
		{"terminator stops scanning", []string{"serve", "--", "migrate"}, true, "127.0.0.1:8090"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsServe(tc.args); got != tc.serve {
				t.Errorf("IsServe(%q) = %v, want %v", tc.args, got, tc.serve)
			}
			if got, _ := ServeAddr(tc.args); got != tc.addr {
				t.Errorf("ServeAddr(%q) = %q, want %q", tc.args, got, tc.addr)
			}
		})
	}
}

// Whatever occupies the listen address before PocketBase starts speaks plain
// HTTP and nothing else, because no server and so no TLS configuration exists
// yet. So the question is not whether TLS is configured somewhere but whether
// anything off this host can reach the port: what the unlock gate collects there
// is the password to the whole archive.
//
// Loopback is the entire test, and it applies to an explicit --http too. An
// earlier version exempted every explicit address on the theory that passing
// --http was a deliberate choice — which exempted the stock container, whose
// entrypoint passes --http=0.0.0.0:${PORT}, and so let the insecure-gate refusal
// never fire on the one configuration most people run.
func TestServeAddrFlagsCleartextAutocertMode(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		addr      string
		cleartext bool
	}{
		{"local default", []string{"serve"}, "127.0.0.1:8090", false},
		{"explicit local http", []string{"serve", "--http", "127.0.0.1:8090"}, "127.0.0.1:8090", false},
		{"localhost by name", []string{"serve", "--http=localhost:8090"}, "localhost:8090", false},
		{"ipv6 loopback", []string{"serve", "--http=[::1]:8090"}, "[::1]:8090", false},
		// What the container's own entrypoint passes. Reachable from the LAN
		// whenever the port is published on 0.0.0.0, which the base compose file
		// does.
		{"all interfaces", []string{"serve", "--http=0.0.0.0:8090"}, "0.0.0.0:8090", true},
		// The spelling most easily mistaken for loopback: an empty host is every
		// interface, not none.
		{"empty host", []string{"serve", "--http=:8090"}, ":8090", true},
		{"autocert domain", []string{"serve", "example.com"}, "0.0.0.0:80", true},
		{"two domains", []string{"serve", "a.example.com", "b.example.com"}, "0.0.0.0:80", true},
		{"domain with explicit local http", []string{"serve", "example.com", "--http", "127.0.0.1:9000"}, "127.0.0.1:9000", false},
		{"domains only reach serve", []string{"migrate", "up"}, "127.0.0.1:8090", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			addr, cleartext := ServeAddr(tc.args)
			if addr != tc.addr {
				t.Errorf("addr = %q, want %q", addr, tc.addr)
			}
			if cleartext != tc.cleartext {
				t.Errorf("cleartext = %v, want %v", cleartext, tc.cleartext)
			}
		})
	}
}

func TestBareSkipsFlagValues(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{
			"flags around the subcommand",
			[]string{"--dir", "/data", "sub", "--http", "127.0.0.1:80", "op", "extra"},
			[]string{"sub", "op", "extra"},
		},
		// The value of a flag must never be read as a subcommand, or
		// `--http op` silently runs `op`.
		{"a flag value spelled like an operand", []string{"serve", "--http", "op"}, []string{"serve"}},
		{"joined values are not operands", []string{"--dir=/data", "serve"}, []string{"serve"}},
		{"bool flags consume nothing", []string{"--dev", "serve"}, []string{"serve"}},
		// Everything after -- belongs to the command being run, not to us.
		{"after a terminator", []string{"--", "sub", "op"}, nil},
		{"nothing at all", nil, nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Bare(tc.args); !slices.Equal(got, tc.want) {
				t.Fatalf("Bare(%q) = %q, want %q", tc.args, got, tc.want)
			}
		})
	}
}

func TestSubcommand(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{nil, ""},
		{[]string{"serve"}, "serve"},
		{[]string{"--dir", "/data", "migrate", "up"}, "migrate"},
		{[]string{"--http", "migrate"}, ""},
		{[]string{"--", "migrate"}, ""},
	}
	for _, tc := range cases {
		if got := Subcommand(tc.args); got != tc.want {
			t.Errorf("Subcommand(%q) = %q, want %q", tc.args, got, tc.want)
		}
	}
}

func TestFlag(t *testing.T) {
	cases := []struct {
		name string
		args []string
		flag string
		want string
	}{
		{"separate value", []string{"--dir", "/data", "serve"}, "--dir", "/data"},
		{"joined value", []string{"--dir=/data", "serve"}, "--dir", "/data"},
		{"absent", []string{"serve"}, "--dir", ""},
		{"empty joined value", []string{"--dir=", "serve"}, "--dir", ""},
		// A different flag's value must not be returned by mistake.
		{"not confused by a neighbour", []string{"--http", "--dir", "serve"}, "--dir", ""},
		{"after a terminator", []string{"--", "--dir", "/data"}, "--dir", ""},
		{"first wins", []string{"--dir", "/a", "--dir", "/b"}, "--dir", "/a"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Flag(tc.args, tc.flag); got != tc.want {
				t.Fatalf("Flag(%q, %q) = %q, want %q", tc.args, tc.flag, got, tc.want)
			}
		})
	}
}

// A help or version flag is not serve, however it is spelled.
//
// Treating `lemmary --help` as serve blocks the process on an unlock form to
// print usage — and, worse, unlocks and restores the archive for a command that
// never bootstraps the databases, which is the one state a flush must not commit
// from. The list mirrors PocketBase's own skipBootstrap, so the two agree about
// which invocations never open a database.
func TestHelpAndVersionAreNotServe(t *testing.T) {
	for _, args := range [][]string{
		{"--help"}, {"-h"}, {"--version"}, {"-v"},
		{"serve", "--help"}, {"--http", "0.0.0.0:80", "--help"},
	} {
		if IsServe(args) {
			t.Errorf("IsServe(%q) = true", args)
		}
		if !HasHelpOrVersionFlag(args) {
			t.Errorf("HasHelpOrVersionFlag(%q) = false", args)
		}
	}

	for _, args := range [][]string{
		nil, {"serve"}, {"--http", "0.0.0.0:80"}, {"--dev", "serve"},
		// A flag's value that happens to read like one must not be mistaken for
		// the flag itself.
		{"serve", "--publicDir", "--help"},
		// Past "--" nothing is a flag any more.
		{"serve", "--", "--help"},
	} {
		if !IsServe(args) {
			t.Errorf("IsServe(%q) = false", args)
		}
	}
}
