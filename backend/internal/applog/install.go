package applog

import (
	"log"
	"log/slog"
	"math"
	"os"
	"reflect"
	"unsafe"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
)

const (
	bootstrapHookID      = "paperlessGoConsoleLogOnBootstrap"
	settingsReloadHookID = "paperlessGoConsoleLogOnSettingsReload"
)

// Register attaches the stdout tee immediately after PocketBase initLogger.
// MaxInt priority makes this the last OnBootstrap handler in the chain so
// Install runs before other hooks unwind and spawn goroutines that call Logger().
func Register(app core.App) {
	app.OnBootstrap().Bind(&hook.Handler[*core.BootstrapEvent]{
		Id:       bootstrapHookID,
		Priority: math.MaxInt,
		Func: func(e *core.BootstrapEvent) error {
			if err := e.Next(); err != nil {
				return err
			}
			Install(e.App)
			return nil
		},
	})
}

// Install tees app.Logger() to stdout at LOG_LEVEL without changing
// PocketBase's logs-table min level. No-op when LOG_LEVEL is unset/invalid
// or when the app is in --dev mode (PocketBase already prints to the console).
func Install(app core.App) {
	if app == nil || app.IsDev() {
		return
	}
	level, ok := levelFromEnv()
	if !ok {
		return
	}
	current := app.Logger()
	inner := current.Handler()
	if _, already := inner.(*teeHandler); already {
		bindSettingsReload(app)
		return
	}
	if innerBatchHandler(inner) == nil {
		log.Printf("applog: LOG_LEVEL is set but the app logger is %T, not a PocketBase BatchHandler; skipping stdout tee", inner)
		return
	}

	console := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	next := slog.New(&teeHandler{console: console, inner: inner})
	if !setAppLogger(app, next) {
		// The reflection swap depends on PocketBase's private logger field; a
		// PB upgrade can silently break it, and losing the stdout tee without
		// a trace makes that miserable to diagnose.
		log.Printf("applog: could not install stdout log tee (PocketBase internals changed?); console logging disabled")
		return
	}
	bindSettingsReload(app)
}

func bindSettingsReload(app core.App) {
	app.OnSettingsReload().Bind(&hook.Handler[*core.SettingsReloadEvent]{
		Id: settingsReloadHookID,
		Func: func(e *core.SettingsReloadEvent) error {
			if err := e.Next(); err != nil {
				return err
			}
			if e.App.IsDev() || e.App.Settings() == nil {
				return nil
			}
			if h := innerBatchHandler(e.App.Logger().Handler()); h != nil {
				h.SetLevel(slog.Level(e.App.Settings().Logs.MinLevel))
			}
			return nil
		},
	})
}

func setAppLogger(app core.App, next *slog.Logger) bool {
	return setLoggerOnValue(reflect.ValueOf(app), next)
}

func setLoggerOnValue(v reflect.Value, next *slog.Logger) bool {
	for v.IsValid() {
		switch v.Kind() {
		case reflect.Interface:
			if v.IsNil() {
				return false
			}
			v = v.Elem()
		case reflect.Pointer:
			if v.IsNil() {
				return false
			}
			elem := v.Elem()
			if elem.Kind() == reflect.Struct {
				if setLoggerField(elem, next) {
					return true
				}
				if f := elem.FieldByName("App"); f.IsValid() {
					v = f
					continue
				}
			}
			v = elem
		case reflect.Struct:
			return setLoggerField(v, next)
		default:
			return false
		}
	}
	return false
}

func setLoggerField(structVal reflect.Value, next *slog.Logger) bool {
	f := structVal.FieldByName("logger")
	if !f.IsValid() || f.Type() != reflect.TypeOf(next) {
		return false
	}
	reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem().Set(reflect.ValueOf(next))
	return true
}
