package watcher

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// File system event
type EventType int

const (
	EventCreate EventType = iota
	EventModify
	EventDelete
	EventRename
)

func (e EventType) String() string {
	switch e {
	case EventCreate:
		return "CREATE"
	case EventModify:
		return "MODIFY"
	case EventDelete:
		return "DELETE"
	case EventRename:
		return "RENAME"
	default:
		return "UNKNOWN"
	}
}

type FileEvent struct {
	Path      string
	Type      EventType
	Timestamp time.Time
}

// Batch of events collected during the debounce window.
type BatchedEvents struct {
	Events    []FileEvent
	BatchedAt time.Time
}

type Config struct {
	DebounceDelay time.Duration
	IgnorePatterns []string // glob patterns for files to ignore
	WatchHidden bool
}

func DefaultConfig() Config {
	return Config{
		DebounceDelay: 500 * time.Millisecond,
		IgnorePatterns: []string{
			"*.swp", "*.swo", "*~", // Editor temp files
			".git/**",              // Git internals
			"node_modules/**",      // Dependencies
			"vendor/**",            // Go vendor
			"*.log",                // Log files
		},
		WatchHidden: false,
	}
}

type Watcher struct {
	config   Config
	fsWatch  *fsnotify.Watcher
	rootPath string

	pendingEvents map[string]*pendingEvent // accumulated events
	pendingMu     sync.Mutex

	debounceTimer *time.Timer

	events chan BatchedEvents
	errors chan error

	done chan struct{}
}

type pendingEvent struct {
	path      string
	eventType EventType
	firstSeen time.Time
	lastSeen  time.Time
}

func New(rootPath string, config Config) (*Watcher, error) {
	absPath, err := filepath.Abs(rootPath)
	if err != nil {
		return nil, err
	}

	fsWatch, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	w := &Watcher{
		config:        config,
		fsWatch:       fsWatch,
		rootPath:      absPath,
		pendingEvents: make(map[string]*pendingEvent),
		events:        make(chan BatchedEvents, 16),
		errors:        make(chan error, 8),
		done:          make(chan struct{}),
	}

	return w, nil
}

func (w *Watcher) Events() <-chan BatchedEvents {
	return w.events
}

func (w *Watcher) Errors() <-chan error {
	return w.errors
}

// Start watching
func (w *Watcher) Start(ctx context.Context) error {
	if err := w.addRecursive(w.rootPath); err != nil {
		return err
	}

	go w.eventLoop(ctx)
	return nil
}

func (w *Watcher) Stop() error {
	close(w.done)
	return w.fsWatch.Close()
}

func (w *Watcher) addRecursive(root string) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip inaccessible paths
		}

		if info.IsDir() {
			// Skip hidden directories if configured
			if !w.config.WatchHidden && len(info.Name()) > 0 && info.Name()[0] == '.' && path != root {
				return filepath.SkipDir
			}

			// Skip ignored patterns
			if w.shouldIgnore(path) {
				return filepath.SkipDir
			}

			return w.fsWatch.Add(path)
		}
		return nil
	})
}

// Checks if a path matches any ignore pattern.
func (w *Watcher) shouldIgnore(path string) bool {
	relPath, err := filepath.Rel(w.rootPath, path)
	if err != nil {
		return false
	}

	for _, pattern := range w.config.IgnorePatterns {
		matched, err := filepath.Match(pattern, relPath)
		if err == nil && matched {
			return true
		}
		// Also check the base name
		matched, err = filepath.Match(pattern, filepath.Base(path))
		if err == nil && matched {
			return true
		}
	}
	return false
}

// Processes fsnotify events
func (w *Watcher) eventLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			w.flushPending()
			return

		case <-w.done:
			w.flushPending()
			return

		case event, ok := <-w.fsWatch.Events:
			if !ok {
				return
			}
			w.handleFSEvent(event)

		case err, ok := <-w.fsWatch.Errors:
			if !ok {
				return
			}
			select {
			case w.errors <- err:
			default:
				// Error channel full, drop error
			}
		}
	}
}

// Processes a single fsnotify event
func (w *Watcher) handleFSEvent(event fsnotify.Event) {
	// Skip ignored files
	if w.shouldIgnore(event.Name) {
		return
	}

	// Convert fsnotify operation to our EventType
	var eventType EventType
	switch {
	case event.Op&fsnotify.Create != 0:
		eventType = EventCreate
		// If a directory was created, add it to the watch
		if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
			_ = w.fsWatch.Add(event.Name)
		}
	case event.Op&fsnotify.Write != 0:
		eventType = EventModify
	case event.Op&fsnotify.Remove != 0:
		eventType = EventDelete
	case event.Op&fsnotify.Rename != 0:
		eventType = EventRename
	default:
		return // Ignore chmod and other events
	}

	w.queueEvent(event.Name, eventType)
}

// Adds an event to the pending batch and resets the debounce timer
func (w *Watcher) queueEvent(path string, eventType EventType) {
	w.pendingMu.Lock()
	defer w.pendingMu.Unlock()

	now := time.Now()

	if existing, ok := w.pendingEvents[path]; ok {
		// Update existing pending event
		existing.lastSeen = now
		// Upgrade event type if needed (create -> modify stays create)
		if existing.eventType != EventCreate {
			existing.eventType = eventType
		}
	} else {
		// Add new pending event
		w.pendingEvents[path] = &pendingEvent{
			path:      path,
			eventType: eventType,
			firstSeen: now,
			lastSeen:  now,
		}
	}

	// Reset or start the debounce timer
	if w.debounceTimer != nil {
		w.debounceTimer.Stop()
	}
	w.debounceTimer = time.AfterFunc(w.config.DebounceDelay, func() {
		w.flushPending()
	})
}

func (w *Watcher) flushPending() {
	w.pendingMu.Lock()
	defer w.pendingMu.Unlock()

	if len(w.pendingEvents) == 0 {
		return
	}

	batch := BatchedEvents{
		Events:    make([]FileEvent, 0, len(w.pendingEvents)),
		BatchedAt: time.Now(),
	}

	for _, pe := range w.pendingEvents {
		batch.Events = append(batch.Events, FileEvent{
			Path:      pe.path,
			Type:      pe.eventType,
			Timestamp: pe.lastSeen,
		})
	}

	// Clear pending events
	w.pendingEvents = make(map[string]*pendingEvent)

	// Emit batch (non-blocking)
	select {
	case w.events <- batch:
	default:
		// Channel full, batch lost
	}
}
