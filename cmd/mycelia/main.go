package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tika/mycelia/internal/watcher"
)

func main() {
	targetPath := flag.String("path", ".", "Path to watch for changes")
	debounceMs := flag.Int("debounce", 500, "Debounce delay in milliseconds")
	flag.Parse()

	info, err := os.Stat(*targetPath)
	if err != nil {
		log.Fatalf("Error: cannot access path %q: %v", *targetPath, err)
	}
	if !info.IsDir() {
		log.Fatalf("Error: %q is not a directory", *targetPath)
	}

	config := watcher.DefaultConfig()
	config.DebounceDelay = config.DebounceDelay * time.Duration(*debounceMs) / 500

	w, err := watcher.New(*targetPath, config)
	if err != nil {
		log.Fatalf("Failed to create watcher: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	if err := w.Start(ctx); err != nil {
		log.Fatalf("Failed to start watcher: %v", err)
	}

	fmt.Printf("Mycelia indexer watching: %s\n", *targetPath)
	fmt.Printf("Debounce delay: %dms\n", *debounceMs)
	fmt.Println("Press Ctrl+C to stop...")
	fmt.Println()

	for {
		select {
		case batch := <-w.Events():
			fmt.Printf("=== Batch received at %s (%d events) ===\n",
				batch.BatchedAt.Format("15:04:05.000"),
				len(batch.Events))

			for _, event := range batch.Events {
				fmt.Printf("  [%s] %s @ %s\n",
					event.Type,
					event.Path,
					event.Timestamp.Format("15:04:05.000"))
			}
			fmt.Println()

		case err := <-w.Errors():
			log.Printf("Watcher error: %v", err)

		case <-sigChan:
			fmt.Println("\nShutting down...")
			if err := w.Stop(); err != nil {
				log.Printf("Error stopping watcher: %v", err)
			}
			return
		}
	}
}
