package ui

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/VeVarunSharma/sodapop/internal/config"
	"github.com/VeVarunSharma/sodapop/internal/engine"
)

type engineLease struct {
	ctx    context.Context
	engine engine.Engine
	cancel context.CancelFunc
	once   sync.Once
	err    error
}

func (l *engineLease) close() error {
	if l == nil {
		return nil
	}
	l.once.Do(func() {
		l.cancel()
		l.err = l.engine.Close()
	})
	return l.err
}

type answer struct {
	allow  bool
	text   string
	cancel bool
}

type decision struct {
	key        string
	sessionID  string
	permission *engine.Permission
	question   *engine.Question
	once       sync.Once
	err        error
	resolved   atomic.Bool
}

func (d *decision) resolve(a answer) error {
	d.once.Do(func() {
		d.resolved.Store(true)
		switch {
		case d.permission != nil && d.permission.Respond != nil:
			d.err = d.permission.Respond(a.allow && !a.cancel)
		case d.question != nil && a.cancel && d.question.Cancel != nil:
			d.err = d.question.Cancel()
		case d.question != nil && !a.cancel && d.question.Respond != nil:
			d.err = d.question.Respond(a.text)
		default:
			d.err = errors.New("the agent supplied a request without a usable response handler")
		}
	})
	if a.cancel && expectedCancellationError(d.err) {
		return nil
	}
	return d.err
}

func expectedCancellationError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, engine.ErrAlreadyResolved)
}

// Requests are registered by the event-reading command, before Update sees them.
// This also releases a request if the program exits with its message in flight.
type resources struct {
	ctx       context.Context
	cancel    context.CancelFunc
	mu        sync.Mutex
	accountMu sync.RWMutex
	closed    bool
	engines   map[*engineLease]struct{}
	factories map[*factoryWork]struct{}
	decisions map[string]*decision
	once      sync.Once
	err       error
	prefs     preferenceWriter
}

func newResources(ctx context.Context, save func(config.Preferences) error) *resources {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	return &resources{
		ctx: ctx, cancel: cancel,
		engines:   make(map[*engineLease]struct{}),
		factories: make(map[*factoryWork]struct{}),
		decisions: make(map[string]*decision),
		prefs:     preferenceWriter{save: save},
	}
}

func (r *resources) adopt(l *engineLease) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return false
	}
	r.engines[l] = struct{}{}
	return true
}

func (r *resources) track(generation uint64, e engine.Event) *decision {
	if e.Permission == nil && e.Question == nil {
		return nil
	}
	id := e.ID
	if e.Permission != nil && e.Permission.ID != "" {
		id = e.Permission.ID
	}
	if e.Question != nil && e.Question.ID != "" {
		id = e.Question.ID
	}
	if id == "" {
		id = fmt.Sprintf("%p/%p", e.Permission, e.Question)
	}
	key := fmt.Sprintf("%d/%s/%s/%s", generation, e.SessionID, e.Kind, id)
	r.mu.Lock()
	if existing := r.decisions[key]; existing != nil {
		r.mu.Unlock()
		return existing
	}
	d := &decision{key: key, sessionID: e.SessionID, permission: e.Permission, question: e.Question}
	r.decisions[key] = d
	closed := r.closed
	r.mu.Unlock()
	if closed {
		_ = d.resolve(answer{cancel: true})
	}
	return d
}

func (r *resources) shutdown() error {
	r.once.Do(func() {
		r.cancel()
		r.mu.Lock()
		r.closed = true
		decisions := make([]*decision, 0, len(r.decisions))
		for _, d := range r.decisions {
			decisions = append(decisions, d)
		}
		engines := make([]*engineLease, 0, len(r.engines))
		for l := range r.engines {
			engines = append(engines, l)
		}
		r.mu.Unlock()
		for _, d := range decisions {
			r.err = errors.Join(r.err, d.resolve(answer{cancel: true}))
		}
		for _, l := range engines {
			r.err = errors.Join(r.err, l.close())
		}
		_, err := r.prefs.flush()
		r.err = errors.Join(r.err, err)
	})
	return r.err
}

// UI updates only take mu, never the disk-I/O lock. Serializing writes and
// keeping the newest snapshot prevents a slow old write from winning a race.
type preferenceWriter struct {
	mu      sync.Mutex
	writeMu sync.Mutex
	save    func(config.Preferences) error
	value   config.Preferences
	version uint64
	saved   uint64
}

func (w *preferenceWriter) set(p config.Preferences) uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.value = clonePreferences(p)
	w.version++
	return w.version
}

func (w *preferenceWriter) flush() (uint64, error) {
	w.writeMu.Lock()
	defer w.writeMu.Unlock()
	for {
		w.mu.Lock()
		p, version, saved := clonePreferences(w.value), w.version, w.saved
		w.mu.Unlock()
		if version == saved {
			return version, nil
		}
		if w.save != nil {
			if err := w.save(p); err != nil {
				return version, err
			}
		}
		w.mu.Lock()
		w.saved = version
		w.mu.Unlock()
	}
}

func clonePreferences(p config.Preferences) config.Preferences {
	if p.ModelSettings == nil {
		return p
	}
	settings := make(map[string]config.ModelSettings, len(p.ModelSettings))
	for model, value := range p.ModelSettings {
		settings[model] = value
	}
	p.ModelSettings = settings
	return p
}
