package bshark

import (
	"context"
	"fmt"
	"reflect"
	"runtime"
	"sync"
	"time"

	"github.com/spf13/pflag"

	"github.com/kkkbird/bshark/debugserver"
	"github.com/kkkbird/qlog"
)

var log = qlog.WithField("bshark", "application")

func getFuncName(f interface{}) string {
	fv := reflect.ValueOf(f)

	if fv.Kind() != reflect.Func {
		return ""
	}
	return runtime.FuncForPC(fv.Pointer()).Name()
}

// DaemonFunc for bshark app daemon modules
type DaemonFunc func(ctx context.Context) error

// InitFunc for bshark app init modules
type InitFunc func(ctx context.Context) error

// ClearFunc for bshark app
type ClearFunc func(ctx context.Context) // TODO: add clear funcs

//InitStage is executed with add sequence, InitFunc in one init stage will be called concurrently
type InitStage struct {
	name  string
	funcs []InitFunc
}

// Run run a InitStage
func (s *InitStage) Run(ctx context.Context, a *Application) error {
	var wg sync.WaitGroup

	wg.Add(len(s.funcs))

	for _, fc := range s.funcs {
		go func(_fc InitFunc) {
			defer wg.Done()

			funcName := getFuncName(_fc)

			defer func() {
				if r := recover(); r != nil {
					a.initErrChan <- fmt.Errorf("%s() panic:%s", funcName, r)
				}
			}()

			log.Tracef("  %s() start...", funcName)

			if err := _fc(ctx); err != nil {
				a.initErrChan <- fmt.Errorf("%s():%s", funcName, err)
				return
			}
			log.Tracef("  %s() done!", funcName)
		}(fc)
	}

	wg.Wait()

	return nil
}

func newInitStage(name string, funcs []InitFunc) *InitStage {
	return &InitStage{
		name:  name,
		funcs: funcs,
	}
}

// Application is a bshark app
type Application struct {
	initTimeout             time.Duration
	initErrChan             chan error
	initForceCloseTimeout   time.Duration // default 1s
	daemonForceCloseTimeout time.Duration // default 3s

	preInit             func()
	onConfigFileChanged func()
	cmdline             *pflag.FlagSet
	name                string
	initStages          []*InitStage
	daemons             []DaemonFunc
}

// AppOpts is setters for application options
type AppOpts func(a *Application)

// func WithName(name string) AppOpts {
// 	return func(a *Application) {
// 		a.name = name
// 	}
// }

// WithInitTimeout set init with a timeout
func WithInitTimeout(timeout time.Duration) AppOpts {
	return func(a *Application) {
		a.initTimeout = timeout
	}
}

// WithInitForceCloseTimeout set init force close timeout
func WithInitForceCloseTimeout(timeout time.Duration) AppOpts {
	return func(a *Application) {
		a.initForceCloseTimeout = timeout
	}
}

// WithDaemonForceCloseTimeout set daemon force close timeout
func WithDaemonForceCloseTimeout(timeout time.Duration) AppOpts {
	return func(a *Application) {
		a.daemonForceCloseTimeout = timeout
	}
}

// WithLogger set logger of application
// func WithLogger(logger Logger) AppOpts {
// 	return func(a *Application) {
// 		a.logger = logger
// 	}
// }

// New create a bshark app object
func New(name string, opts ...AppOpts) *Application {
	app := &Application{
		initTimeout:             0, // no timeout
		initForceCloseTimeout:   time.Second,
		daemonForceCloseTimeout: 3 * time.Second,
		cmdline:                 pflag.CommandLine,
		name:                    name,
		initStages:              make([]*InitStage, 0),
		daemons:                 make([]DaemonFunc, 0),

		initErrChan: make(chan error, 1),
	}

	for _, opt := range opts {
		opt(app)
	}

	app.AddInitStage("preInit", app.initParams).AddDaemons(debugserver.Run)

	return app
}

func (a *Application) initParams(ctx context.Context) error {
	a.handleFlagsAndEnv()
	return nil
}

// func (a *Application) printf(format string, args ...interface{}) {
// 	if a.logger == nil {
// 		log.Infof(format, args...)
// 		return
// 	}

// 	a.logger.Printf(format, args...)
// }

// AddInitStage add a stage for bshark app
func (a *Application) AddInitStage(name string, funcs ...InitFunc) *Application {
	a.initStages = append(a.initStages, newInitStage(name, funcs))
	return a
}

// AddDaemons add a daemon for bshark app
func (a *Application) AddDaemons(funcs ...DaemonFunc) *Application {
	a.daemons = append(a.daemons, funcs...)
	return a
}

func (a *Application) runInitStages() error {
	var (
		ctx    = context.Background()
		cancel context.CancelFunc
		err    error
	)

	if a.initTimeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, a.initTimeout)
	} else {
		ctx, cancel = context.WithCancel(ctx)
	}
	defer cancel()

	// run init stages
	for i, s := range a.initStages {
		cErr := make(chan error, 1)

		go func() {
			log.Infof("Init stage %d-%s", i, s.name)
			cErr <- s.Run(ctx, a)
		}()

		select {
		case err = <-cErr:
			if err != nil {
				return err
			}
		case err = <-a.initErrChan:
			log.WithError(err).Errorf("!!Init err, exit in %s ...", a.initForceCloseTimeout.String())
			cancel()
			select { // wait the init stage done or initForceCloseTimeout duration
			case <-cErr:
			case <-time.After(a.initForceCloseTimeout):
			}
			return err
		case <-ctx.Done():
			log.Errorf("!!Init timeount, exit in %s ...", a.initForceCloseTimeout.String())
			select { // wait the init stage done or initForceCloseTimeout duration
			case <-cErr:
			case <-time.After(a.initForceCloseTimeout):
			}
			return ctx.Err()
		}
	}

	return nil
}

func (a *Application) runDaemons() error {

	var (
		ctx    context.Context
		cancel context.CancelFunc
		cErr   = make(chan error, 1)
		cDone  = make(chan interface{}, 1)
	)

	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup

	// run daemon funcs
	go func() {
		wg.Add(len(a.daemons))

		for _, d := range a.daemons {
			go func(_d DaemonFunc) {
				defer wg.Done()

				funcName := getFuncName(_d)

				defer func() {
					if r := recover(); r != nil {
						cErr <- fmt.Errorf("%s() panic:%s", funcName, r)
					}
				}()

				log.Tracef("  %s() ... running", funcName)
				if err := _d(ctx); err != nil {
					cErr <- fmt.Errorf("%s():%s", funcName, err)
					return
				}
				log.Tracef("  %s() ... done", funcName)
			}(d)
		}

		wg.Wait()

		close(cDone)
	}()

	var err error
	var isCanceled = false

__daemon_loop:
	for {
		var closeTimer <-chan time.Time
		if isCanceled {
			closeTimer = time.After(a.daemonForceCloseTimeout)
		} else {
			closeTimer = nil
		}

		select {
		case err = <-cErr:
			log.WithError(err).Errorf("!!Daemon err, exit in %s ...", a.daemonForceCloseTimeout.String())
			cancel()
			isCanceled = true
		case <-closeTimer:
			log.Infof("!!Daemon exit after %s", a.daemonForceCloseTimeout.String())
			break __daemon_loop
		case <-cDone:
			break __daemon_loop
		}
	}
	return err
}

// Run run bshark app, it should be called at last
func (a *Application) Run() {
	var err error
	log.Infof("Application %s starting...", a.name)

	if err = a.runInitStages(); err != nil {
		panic(err)
	}

	log.Infof("All init stage done, starting daemons...")

	if err = a.runDaemons(); err != nil {
		panic(err)
	}
	log.Infof("App %s done", a.name)
}
