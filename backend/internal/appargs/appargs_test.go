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

// With domain arguments PocketBase serves HTTPS on :443 via autocert and uses
// :80 only to redirect — but before the server starts nothing is listening on
// :443, so a browser reaching :80 has no TLS to fall back to. Anything that
// occupies that address first has to be able to tell, because what it collects
// there would travel in the clear.
func TestServeAddrFlagsCleartextAutocertMode(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		addr      string
		cleartext bool
	}{
		{"local default", []string{"serve"}, "127.0.0.1:8090", false},
		{"explicit local http", []string{"serve", "--http", "127.0.0.1:8090"}, "127.0.0.1:8090", false},
		{"behind a proxy", []string{"serve", "--http=0.0.0.0:8090"}, "0.0.0.0:8090", false},
		{"autocert domain", []string{"serve", "example.com"}, "0.0.0.0:80", true},
		{"two domains", []string{"serve", "a.example.com", "b.example.com"}, "0.0.0.0:80", true},
		// An explicit --http means the operator chose the address, so autocert
		// is not in play and this is not the code making it cleartext.
		{"domain with explicit http", []string{"serve", "example.com", "--http", "127.0.0.1:9000"}, "127.0.0.1:9000", false},
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
