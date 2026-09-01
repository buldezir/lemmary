package vault

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// The unlock gate is a small HTTP server that runs *before* PocketBase exists.
//
// It has to: the users collection and its password hashes live inside the
// encrypted database, so nothing in PocketBase can authenticate anyone until the
// database has already been decrypted. The keyring is what breaks that circle —
// it lets a credential be checked, and the master key recovered, without opening
// anything.
//
// The page it serves is deliberately hand-written rather than the SPA: public/
// is served by PocketBase, which is not running yet.

// GateResult reports how the gate finished.
type GateResult struct {
	// Initialized is true when the gate created a brand new vault.
	Initialized bool
	// RecoveryCode is set only on first initialisation, and shown once.
	RecoveryCode string
}

// Gate blocks until the vault is unlocked, then returns.
//
// It listens on addr, which must be the address PocketBase will later bind, and
// releases it before returning so the handover can happen.
//
// insecure reports that addr is reachable from off this host while carrying
// cleartext HTTP. The gate refuses to collect a credential in that case unless
// the operator has explicitly accepted it: this form submits the password that
// unwraps the master key for the whole archive, and nothing is serving TLS while
// the instance is locked — PocketBase is not running yet, so under autocert
// there is not even anything listening on 443 to fall back to.
func (v *Vault) Gate(ctx context.Context, addr string, insecure bool) (GateResult, error) {
	var result GateResult

	if !v.Enabled() {
		return result, nil
	}

	// A non-interactive unlock, for CLI subcommands and automated tests.
	if pass := os.Getenv(EnvPassphrase); pass != "" {
		if !v.Initialized() {
			code, err := v.Init("", pass)
			if err != nil {
				return result, err
			}
			result.Initialized, result.RecoveryCode = true, code
			return result, nil
		}
		err := v.Unlock(Credential{Password: pass})
		if err == nil {
			return result, nil
		}
		if !errors.Is(err, ErrWrongKey) {
			return result, err
		}
		// A passphrase that no longer opens anything must not be fatal here.
		// The expected way to reach this is not a typo: provisioning with
		// VAULT_PASSPHRASE set is documented, and the first account save
		// deliberately revokes the bootstrap wrap that passphrase created. Leave
		// the variable in the compose file — the natural thing to do — and the
		// next restart would fail startup, exit 1, and crash-loop the container
		// under any restart policy, with the unlock form that would have
		// accepted an ordinary account password never served. Fall through to
		// the gate instead; a human at a browser can still get in.
		v.opts.Log("vault: %s did not unlock the archive (it is probably the bootstrap passphrase, which is revoked once an account is enrolled); waiting for a sign-in instead", EnvPassphrase)
	}

	if insecure && !v.opts.AllowInsecureGate {
		return result, fmt.Errorf(
			"vault: refusing to serve the unlock form on %s. That form carries the password which decrypts the "+
				"whole archive, this gate speaks plain HTTP (it runs before the server exists, so it has no TLS to "+
				"use), and %s is not a loopback address — so who can reach it is decided outside this process and "+
				"cannot be checked from in here. Either bind loopback directly (serve --http 127.0.0.1:8090) and "+
				"put TLS in front, or, in a container where binding 0.0.0.0 is unavoidable, publish the port on "+
				"127.0.0.1 only and set %s=1 to say so — which is exactly what docker-compose.encrypted.yml does",
			addr, addr, EnvAllowInsecureGate)
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return result, fmt.Errorf("vault: unlock gate cannot listen on %s: %w", addr, err)
	}

	done := make(chan GateResult, 1)
	srv := &http.Server{
		Handler:           v.gateHandler(done),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			v.opts.Log("vault: unlock gate stopped: %v", err)
		}
	}()

	v.opts.Log("vault: locked — waiting for a sign-in on %s to unlock the archive", addr)

	select {
	case <-ctx.Done():
		_ = srv.Close()
		return result, ctx.Err()
	case result = <-done:
	}

	// Hand the port back. Shutdown waits for the response to reach the browser,
	// then the listener is closed before PocketBase tries to bind it.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		_ = srv.Close()
	}
	return result, nil
}

type unlockRequest struct {
	Password     string `json:"password"`
	RecoveryCode string `json:"recovery_code"`
}

func (v *Vault) gateHandler(done chan<- GateResult) http.Handler {
	mux := http.NewServeMux()

	// A locked instance must not look healthy-but-empty to the API, or a client
	// would render an archive with no documents in it. 423 is unambiguous.
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusLocked, map[string]any{
			"status":  http.StatusLocked,
			"message": "This instance is encrypted and locked. Sign in to unlock it.",
			"locked":  true,
		})
	})

	// The container healthcheck must treat locked as healthy: an instance
	// waiting for its first sign-in is working as designed, and failing here
	// would make Docker restart-loop it forever.
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"code":    http.StatusOK,
			"message": "Locked, awaiting unlock.",
			"data":    map[string]any{"locked": true, "initialized": v.Initialized()},
		})
	})

	mux.HandleFunc("/unlock", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"message": "POST required."})
			return
		}
		if err := checkSameOrigin(r); err != nil {
			// Initialising an empty vault mints a master key under a
			// caller-chosen password, and unlocking is the one operation that
			// matters here, so neither may be reachable by cross-origin script.
			// JSON-only forces a CORS preflight this server never answers, and
			// the fetch-metadata check rejects anything a browser labels
			// cross-site.
			writeJSON(w, http.StatusForbidden, map[string]string{"message": err.Error()})
			return
		}

		var req unlockRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"message": "Expected a JSON body."})
			return
		}

		// Init is not idempotent, so the check and the call have to be atomic:
		// two racing requests would otherwise save one keyring while the process
		// encrypted under the other's master key.
		v.gateMu.Lock()
		defer v.gateMu.Unlock()

		if !v.Initialized() {
			if req.Password == "" {
				writeJSON(w, http.StatusBadRequest, map[string]string{"message": "A password is required to initialise this instance."})
				return
			}
			code, err := v.Init("", req.Password)
			if err != nil {
				v.opts.Log("vault: initialisation failed: %v", err)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"message": "Initialisation failed."})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"ok":            true,
				"initialized":   true,
				"recovery_code": code,
				"message":       "Write this recovery code down now. It is shown once and it is the only way back in if the password is lost.",
			})
			done <- GateResult{Initialized: true, RecoveryCode: code}
			return
		}

		err := v.Unlock(Credential{Password: req.Password, RecoveryCode: req.RecoveryCode})
		if err != nil {
			// Deliberately uniform: a wrong password and a tampered keyring are
			// the same answer, and the delay blunts online guessing without
			// pretending to replace the Argon2 cost.
			time.Sleep(500 * time.Millisecond)
			status := http.StatusUnauthorized
			msg := "That credential did not unlock this instance."
			if !errors.Is(err, ErrWrongKey) {
				v.opts.Log("vault: unlock failed: %v", err)
				status, msg = http.StatusInternalServerError, "Unlock failed."
			}
			writeJSON(w, status, map[string]string{"message": msg})
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		done <- GateResult{}
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_ = unlockPage.Execute(w, map[string]any{"Initialized": v.Initialized()})
	})

	return mux
}

// checkSameOrigin rejects requests a browser would let a hostile page make.
//
// The unlock endpoint has no session, so SameSite cookies protect nothing. Two
// cheap checks cover it: requiring a JSON content type means a cross-origin
// caller must send a preflight, which this server never approves, and
// Sec-Fetch-Site catches browsers that send the request anyway. Non-browser
// clients (curl, the CLI) send neither header and are unaffected.
func checkSameOrigin(r *http.Request) error {
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		return errors.New("Send this request as application/json.")
	}
	switch r.Header.Get("Sec-Fetch-Site") {
	case "", "same-origin", "same-site", "none":
	default:
		return errors.New("Cross-site requests are not accepted here.")
	}
	if origin := r.Header.Get("Origin"); origin != "" && r.Host != "" {
		if u, err := url.Parse(origin); err == nil && u.Host != "" && u.Host != r.Host {
			return errors.New("Cross-origin requests are not accepted here.")
		}
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

var unlockPage = template.Must(template.New("unlock").Parse(`<!doctype html>
<html lang="en"><head>
<meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Locked</title>
<style>
  :root { color-scheme: light dark; --fg:#1a1614; --bg:#f6f2ea; --muted:#6b625a; --line:#ddd4c6; --accent:#7a4a2b; }
  @media (prefers-color-scheme: dark) { :root { --fg:#ece5da; --bg:#17140f; --muted:#a2988a; --line:#3a332a; --accent:#c98f5f; } }
  * { box-sizing: border-box; }
  body { margin:0; min-height:100vh; display:grid; place-items:center; padding:1.5rem;
         background:var(--bg); color:var(--fg);
         font:16px/1.55 ui-serif, Georgia, "Times New Roman", serif; }
  main { width:100%; max-width:26rem; }
  h1 { font-size:1.35rem; margin:0 0 .35rem; letter-spacing:-.01em; }
  p { color:var(--muted); margin:0 0 1.4rem; font-size:.92rem; }
  label { display:block; font-size:.8rem; text-transform:uppercase; letter-spacing:.08em;
          color:var(--muted); margin-bottom:.4rem; }
  input { width:100%; padding:.7rem .8rem; margin-bottom:1rem; border:1px solid var(--line);
          border-radius:2px; background:transparent; color:inherit; font:inherit; }
  input:focus { outline:2px solid var(--accent); outline-offset:1px; }
  button { width:100%; padding:.7rem; border:0; border-radius:2px; background:var(--accent);
           color:#fff; font:inherit; cursor:pointer; }
  button[disabled] { opacity:.6; cursor:progress; }
  .msg { margin-top:1rem; padding:.7rem .8rem; border-left:3px solid var(--accent);
         font-size:.88rem; white-space:pre-wrap; word-break:break-word; display:none; }
  details { margin-top:1rem; font-size:.85rem; color:var(--muted); }
  summary { cursor:pointer; }
</style>
</head><body><main>
{{if .Initialized}}
  <h1>This archive is locked</h1>
  <p>Its contents are encrypted on disk. Sign in to decrypt them for this session.</p>
{{else}}
  <h1>Set up encryption</h1>
  <p>Choose the password that will unlock this archive. You will be given a recovery code — it is shown once.</p>
{{end}}
<form id="f">
  <label for="password">Password</label>
  <input id="password" name="password" type="password" autocomplete="{{if .Initialized}}current-password{{else}}new-password{{end}}" autofocus required>
  {{if .Initialized}}
  <details><summary>Use a recovery code instead</summary>
    <label for="recovery_code" style="margin-top:.8rem">Recovery code</label>
    <input id="recovery_code" name="recovery_code" autocomplete="off" spellcheck="false">
  </details>
  {{end}}
  <button type="submit">Unlock</button>
</form>
<div class="msg" id="m"></div>
<script>
const f = document.getElementById('f'), m = document.getElementById('m');
f.addEventListener('submit', async (ev) => {
  ev.preventDefault();
  const btn = f.querySelector('button');
  btn.disabled = true; m.style.display = 'none';
  try {
    const body = {
      password: document.getElementById('password').value,
      recovery_code: (document.getElementById('recovery_code') || {}).value || ''
    };
    const r = await fetch('/unlock', {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify(body)
    });
    const d = await r.json().catch(() => ({}));
    if (r.ok) {
      if (d.recovery_code) {
        m.textContent = d.message + '\n\n' + d.recovery_code;
        m.style.display = 'block';
        btn.textContent = 'Continue';
        btn.disabled = false;
        f.onsubmit = () => location.reload();
        return;
      }
      m.textContent = 'Unlocked. Starting…';
      m.style.display = 'block';
      setTimeout(() => location.reload(), 1500);
      return;
    }
    m.textContent = d.message || 'Unlock failed.';
    m.style.display = 'block';
  } catch (e) {
    m.textContent = 'Unlock failed.';
    m.style.display = 'block';
  }
  btn.disabled = false;
});
</script>
</main></body></html>`))
