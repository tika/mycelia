package indexer

import (
	"context"
	"fmt"
	"log"
	"sync"

	"github.com/tika/mycelia/internal/mmap"
	"github.com/tika/mycelia/internal/parser"
	"github.com/tika/mycelia/internal/watcher"
)

// IndexResult represents the result of indexing a single file
type IndexResult struct {
	FilePath    string
	EventType   string
	ParseResult *parser.ParseResult
	Error       error
	Skipped     bool
}

// Indexer coordinates file watching and parsing
type Indexer struct {
	watcher *watcher.Watcher
	parser  *parser.Parser
	mmap    *mmap.Reader

	results chan IndexResult
	done    chan struct{}
	wg      sync.WaitGroup
}

func New(rootPath string, watcherConfig watcher.Config) (*Indexer, error) {
	w, err := watcher.New(rootPath, watcherConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create watcher: %w", err)
	}

	return &Indexer{
		watcher: w,
		parser:  parser.New(),
		mmap:    mmap.NewReader(),
		results: make(chan IndexResult, 64),
		done:    make(chan struct{}),
	}, nil
}

// Results returns the channel that emits indexing results
func (idx *Indexer) Results() <-chan IndexResult {
	return idx.results
}

// Start begins watching and indexing files
func (idx *Indexer) Start(ctx context.Context) error {
	if err := idx.watcher.Start(ctx); err != nil {
		return fmt.Errorf("failed to start watcher: %w", err)
	}

	idx.wg.Add(1)
	go idx.processEvents(ctx)

	return nil
}

func (idx *Indexer) Stop() error {
	close(idx.done)
	idx.wg.Wait()
	idx.parser.Close()
	idx.mmap.Close()
	return idx.watcher.Stop()
}

// processEvents handles batched file events from the watcher
func (idx *Indexer) processEvents(ctx context.Context) {
	defer idx.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return

		case <-idx.done:
			return

		case batch, ok := <-idx.watcher.Events():
			if !ok {
				return
			}
			idx.processBatch(ctx, batch)

		case err, ok := <-idx.watcher.Errors():
			if !ok {
				return
			}
			log.Printf("Watcher error: %v", err)
		}
	}
}

func (idx *Indexer) processBatch(ctx context.Context, batch watcher.BatchedEvents) {
	for _, event := range batch.Events {
		eventType := event.Type.String()

		// Invalidate mmap cache for any file change
		idx.mmap.Invalidate(event.Path)

		if event.Type == watcher.EventDelete || event.Type == watcher.EventRename {
			idx.results <- IndexResult{
				FilePath:  event.Path,
				EventType: eventType,
				Error:     fmt.Errorf("file %s", event.Type),
			}
			continue
		}

		lang := parser.DetectLanguage(event.Path)
		if lang == parser.LangUnknown {
			idx.results <- IndexResult{
				FilePath:  event.Path,
				EventType: eventType,
				Skipped:   true,
			}
			continue
		}

		result, err := idx.parser.ParseFile(ctx, event.Path)
		idx.results <- IndexResult{
			FilePath:    event.Path,
			EventType:   eventType,
			ParseResult: result,
			Error:       err,
		}
	}
}

func (idx *Indexer) IndexFile(ctx context.Context, filePath string) (*parser.ParseResult, error) {
	return idx.parser.ParseFile(ctx, filePath)
}

// Returns the raw bytes at [start:end) from the file via mmap
func (idx *Indexer) ReadSlice(path string, start, end uint) ([]byte, error) {
	return idx.mmap.Slice(path, start, end)
}

// Returns the source code for a symbol using its byte offsets
func (idx *Indexer) ReadSymbol(path string, sym parser.Symbol) (string, error) {
	return idx.mmap.SliceString(path, sym.StartByte, sym.EndByte)
}
