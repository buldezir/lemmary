// Package appargs reads the command line the way PocketBase's own cobra setup
// will, before cobra has had a chance to parse it.
//
// Anything that has to act before app.Execute runs — placing the data
// directory, occupying the address the server is about to take, answering a
// subcommand with no database open — needs to know the subcommand and the
// listen address at a point where cobra cannot be asked, because asking cobra
// means bootstrapping the app, which is the thing being deferred.
//
// The conventions mirrored here are cobra's and PocketBase's, not this
// package's invention: a flag's value is never mistaken for a subcommand, "--"
// ends flag parsing, and bare arguments after `serve` are autocert domains.
// Keeping that in one tested package upstream is deliberate — the same logic
// spread across callers, or carried in a fork, rots silently the next time
// PocketBase adds a flag that takes a value.
package appargs

import "strings"

// valueFlags are the flags that consume the following argument, so scanning
// does not mistake a flag's value for the subcommand or vice versa.
//
// PocketBase's own persistent flags plus the two this binary adds. Boolean
// flags (--dev, --indexFallback) are absent on purpose: they never take a
// separate value, and listing one here would swallow the argument after it.
var valueFlags = map[string]bool{
	"--dir": true, "--encryptionEnv": true, "--queryTimeout": true,
	"--http": true, "--https": true, "--origins": true, "--publicDir": true,
}

// Bare returns the non-flag arguments, in order.
//
// The first is the subcommand; a second is that subcommand's own operand. Use
// it when a subcommand has to be recognised before cobra exists.
func Bare(args []string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			break
		}
		if strings.HasPrefix(a, "-") {
			if !strings.Contains(a, "=") && valueFlags[a] {
				i++ // consume the value
			}
			continue
		}
		out = append(out, a)
	}
	return out
}

// Subcommand returns the first bare argument, or "" for PocketBase's default.
func Subcommand(args []string) string {
	sub, _ := scan(args)
	return sub
}

// Flag returns the value of a value-taking flag in either spelling
// (--name value or --name=value), or "" when it is absent.
func Flag(args []string, name string) string {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			break
		}
		if !strings.HasPrefix(a, "-") {
			continue
		}
		if key, val, ok := strings.Cut(a, "="); ok {
			if key == name {
				return val
			}
			continue
		}
		if a == name && i+1 < len(args) {
			return args[i+1]
		}
		if valueFlags[a] {
			i++
		}
	}
	return ""
}

// IsServe reports whether this invocation will start the web server.
//
// An empty subcommand is PocketBase's default, which is serve. Callers use this
// to tell an interactive path from a one-shot: a CLI subcommand must not block
// on something a person is expected to answer, because nobody is watching.
func IsServe(args []string) bool {
	sub, _ := scan(args)
	return sub == "" || sub == "serve"
}

// ServeAddr returns the address the server will listen on, and whether that
// address will carry cleartext HTTP.
//
// With domain arguments PocketBase switches to autocert: HTTPS on :443, with
// :80 used only to redirect. The plain port is still where a browser lands
// first, so anything occupying the address before the server starts gets it —
// and is told the connection is unencrypted, which is a fact a caller may need
// to refuse on.
func ServeAddr(args []string) (addr string, cleartext bool) {
	sub, explicit := scan(args)
	if explicit != "" {
		return explicit, false
	}
	if (sub == "" || sub == "serve") && hasDomainArgs(args, sub) {
		return "0.0.0.0:80", true
	}
	return "127.0.0.1:8090", false
}

// scan walks argv once, yielding the subcommand and the value of --http.
func scan(args []string) (subcommand, httpAddr string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			break
		}
		if name, val, ok := strings.Cut(a, "="); ok && strings.HasPrefix(a, "-") {
			if name == "--http" {
				httpAddr = val
			}
			continue
		}
		if strings.HasPrefix(a, "-") {
			if valueFlags[a] && i+1 < len(args) {
				if a == "--http" {
					httpAddr = args[i+1]
				}
				i++ // consume the value so it is not read as the subcommand
			}
			continue
		}
		if subcommand == "" {
			subcommand = a
		}
	}
	return subcommand, httpAddr
}

// hasDomainArgs reports whether any bare argument after the subcommand looks
// like a domain, which is what switches PocketBase into autocert mode.
//
// Only serve accepts domains — for any other subcommand the trailing arguments
// are its own operands, not hostnames.
func hasDomainArgs(args []string, sub string) bool {
	seenSub := sub == ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			break
		}
		if strings.HasPrefix(a, "-") {
			if !strings.Contains(a, "=") && valueFlags[a] {
				i++
			}
			continue
		}
		if !seenSub {
			seenSub = true
			continue
		}
		return true
	}
	return false
}
