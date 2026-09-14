// Package appargs reads the command line the way PocketBase's cobra setup will,
// before cobra has parsed it.
//
// Anything acting before app.Execute (placing the data directory, occupying the
// listen address, answering a subcommand with no database open) needs the
// subcommand and the address at a point where asking cobra would mean
// bootstrapping the app, which is the thing being deferred. The conventions
// mirrored here are cobra's and PocketBase's: a flag's value is never a
// subcommand, "--" ends flag parsing, bare arguments after serve are autocert
// domains.
package appargs

import (
	"net"
	"strings"
)

// valueFlags consume the following argument, so scanning does not mistake a
// flag's value for the subcommand. Boolean flags are absent on purpose:
// listing one here would swallow the argument after it.
var valueFlags = map[string]bool{
	"--dir": true, "--encryptionEnv": true, "--queryTimeout": true,
	"--http": true, "--https": true, "--origins": true, "--publicDir": true,
}

// Bare returns the non-flag arguments, in order: the subcommand first, then its
// own operands.
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

// Subcommand returns the first bare argument, "" for PocketBase's default (serve).
func Subcommand(args []string) string {
	sub, _ := scan(args)
	return sub
}

// Flag returns the value of a value-taking flag in either spelling
// (--name value or --name=value), or "" when absent.
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

// IsServe reports whether this invocation will start the web server, which is
// what tells an interactive path from a one-shot that must not block on a
// person. A help or version flag also leaves the subcommand empty but is not
// serve: treating `lemmary --help` as serve would unlock and restore the
// archive for a command that never bootstraps the databases, which is the one
// state a flush must not commit from.
func IsServe(args []string) bool {
	if HasHelpOrVersionFlag(args) {
		return false
	}
	sub, _ := scan(args)
	return sub == "" || sub == "serve"
}

// helpOrVersionFlags mirrors PocketBase's own skipBootstrap: an invocation
// carrying one of these never opens a database.
var helpOrVersionFlags = map[string]bool{
	"-h": true, "--help": true, "-v": true, "--version": true,
}

// HasHelpOrVersionFlag reports whether argv asks cobra to print and exit.
func HasHelpOrVersionFlag(args []string) bool {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			return false
		}
		if !strings.HasPrefix(a, "-") {
			continue
		}
		if helpOrVersionFlags[a] {
			return true
		}
		if !strings.Contains(a, "=") && valueFlags[a] {
			i++ // a flag's value is not a flag
		}
	}
	return false
}

// ServeAddr returns the address the server will listen on, and whether reaching
// it means sending cleartext over a network.
//
// The gate occupying this address before PocketBase starts speaks plain HTTP
// only, since no TLS configuration exists yet. Loopback is therefore the whole
// test, explicit --http included: the stock container's entrypoint passes
// --http=0.0.0.0:${PORT}, so exempting explicit addresses would excuse the one
// configuration most people run. With domain arguments PocketBase uses autocert
// on :443 and :80 only redirects, but :80 is still where a browser lands first.
func ServeAddr(args []string) (addr string, cleartext bool) {
	sub, explicit := scan(args)
	if explicit != "" {
		return explicit, !isLoopbackAddr(explicit)
	}
	if (sub == "" || sub == "serve") && hasDomainArgs(args, sub) {
		return "0.0.0.0:80", true
	}
	return "127.0.0.1:8090", false
}

// isLoopbackAddr reports whether a listen address is reachable only from this
// host. An empty host (":8090") is every interface, not none. An unresolvable
// name counts as exposed: guessing wrong the other way serves the archive's
// password over the network.
func isLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	host = strings.Trim(host, "[]")
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

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

// hasDomainArgs reports whether a bare argument after the subcommand looks like
// a domain, which switches PocketBase into autocert mode. Only serve accepts
// domains; elsewhere trailing arguments are the subcommand's own operands.
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
